//
//  PacketTunnelProvider.swift
//  JVPNPacketTunnel
//

import Darwin
import Foundation
import Network
import NetworkExtension
import Security

private enum TunnelConfigKey {
    static let host = "host"
    static let port = "port"
    static let token = "token"
    static let acceptInsecureTLS = "acceptInsecureTLS"
    static let transport = "transport"
    static let wsPath = "wsPath"
}

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private struct BackendConfig {
        let host: String
        let port: UInt16
        let token: String
        let acceptInsecureTLS: Bool
        let transportMode: String
        let wsPath: String
    }

    private var vpnConnection: NWConnection?
    private let ioQueue = DispatchQueue(label: "org.jackh54.jvpn.packet-tunnel.io")
    private var isClosing = false
    private let startLock = NSLock()
    private var startCompleted = false
    private var runtimeStopped = false
    private var useWebSocket = false
    private var wsRecvBuffer = Data()
    private var backendConfig: BackendConfig?
    private var reconnectWorkItem: DispatchWorkItem?
    private var heartbeatWorkItem: DispatchWorkItem?
    private var telemetryPollWorkItem: DispatchWorkItem?
    private var reconnectAttempt = 0
    private var stickyTransportFailures = 0
    private let transportFlipAfterFailures = 5
    private var transportCandidates: [String] = ["ws"]
    private var transportIndex = 0
    private var activeTransport = "ws"
    private var currentConnectionID: UInt64 = 0
    private var reconnectScheduled = false
    private var lastAppliedClientIP: String?
    private var lastAppliedPrefixLen: Int?
    private var lastSentTelemetryRevision = -1
    /// NWConnection allows only one outstanding send; heartbeat/telemetry must not race the data path.
    private var sendQueue: [(conn: NWConnection, data: Data, done: (Error?) -> Void)] = []
    private var sendInFlight = false

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        guard let proto = protocolConfiguration as? NETunnelProviderProtocol,
              let cfg = proto.providerConfiguration,
              let host = cfg[TunnelConfigKey.host] as? String,
              let tokenRaw = cfg[TunnelConfigKey.token] as? String
        else {
            completionHandler(NSError(domain: "JVPN", code: 1, userInfo: [NSLocalizedDescriptionKey: "Missing tunnel configuration"]))
            return
        }

        let token = Self.normalizeSharedToken(tokenRaw)
        guard !token.isEmpty else {
            completionHandler(NSError(domain: "JVPN", code: 1, userInfo: [NSLocalizedDescriptionKey: "Token is empty"]))
            return
        }

        let config = BackendConfig(
            host: host,
            port: (cfg[TunnelConfigKey.port] as? NSNumber)?.uint16Value ?? 443,
            token: token,
            acceptInsecureTLS: (cfg[TunnelConfigKey.acceptInsecureTLS] as? NSNumber)?.boolValue ?? false,
            transportMode: (cfg[TunnelConfigKey.transport] as? String)?.lowercased() ?? "ws",
            wsPath: Self.normalizedWSPath((cfg[TunnelConfigKey.wsPath] as? String) ?? "/ws")
        )
        backendConfig = config
        isClosing = false
        startLock.lock()
        startCompleted = false
        lastAppliedClientIP = nil
        lastAppliedPrefixLen = nil
        reconnectAttempt = 0
        stickyTransportFailures = 0
        reconnectScheduled = false
        runtimeStopped = false
        lastSentTelemetryRevision = -1
        startLock.unlock()
        resetSendQueue(cancelPending: true)
        cancelHeartbeatAndTelemetry()
        let preferred = Self.loadPreferredTransport(host: config.host, port: config.port)
        transportCandidates = Self.resolveTransportCandidates(config.transportMode, preferred: preferred)
        transportIndex = 0
        activeTransport = transportCandidates[transportIndex]
        connectBackend(config: config, isInitial: true, completionHandler: completionHandler)
    }

    private func connectBackend(config: BackendConfig, isInitial: Bool, completionHandler: @escaping (Error?) -> Void) {
        activeTransport = transportCandidates[min(transportIndex, max(0, transportCandidates.count - 1))]
        useWebSocket = (activeTransport == "ws")
        wsRecvBuffer.removeAll(keepingCapacity: true)
        reconnectWorkItem?.cancel()
        reconnectWorkItem = nil
        resetSendQueue(cancelPending: true)
        cancelHeartbeatAndTelemetry()

        let tcp = NWProtocolTCP.Options()
        tcp.enableKeepalive = true
        tcp.keepaliveIdle = 20
        tcp.keepaliveInterval = 5
        tcp.keepaliveCount = 3
        tcp.noDelay = true
        let tls = NWProtocolTLS.Options()
        let sec = tls.securityProtocolOptions
        sec_protocol_options_set_min_tls_protocol_version(sec, .TLSv13)
        if config.acceptInsecureTLS {
            sec_protocol_options_set_verify_block(sec, { _, _, complete in complete(true) }, ioQueue)
        }
        if IPv4Address(config.host) == nil && IPv6Address(config.host) == nil {
            config.host.withCString { sec_protocol_options_set_tls_server_name(sec, $0) }
        }

        let params = NWParameters(tls: tls, tcp: tcp)
        if useWebSocket {
            let ws = NWProtocolWebSocket.Options()
            ws.autoReplyPing = true
            ws.maximumMessageSize = 1024 * 1024
            params.defaultProtocolStack.applicationProtocols.insert(ws, at: 0)
        }

        let endpoint: Network.NWEndpoint
        if useWebSocket {
            guard let url = URL(string: "wss://\(config.host):\(config.port)\(config.wsPath)") else {
                let err = NSError(domain: "JVPN", code: 10, userInfo: [NSLocalizedDescriptionKey: "Invalid websocket URL"])
                if isInitial { finishStart(err, completionHandler) }
                return
            }
            endpoint = .url(url)
        } else {
            endpoint = .hostPort(host: .init(config.host), port: .init(integerLiteral: config.port))
        }

        JVPNDebugLog.tunnel("backend connect host=\(config.host) port=\(config.port) transport=\(activeTransport) mode=\(config.transportMode)")
        let conn = NWConnection(to: endpoint, using: params)
        startLock.lock()
        currentConnectionID &+= 1
        let connID = currentConnectionID
        reconnectScheduled = false
        startLock.unlock()
        vpnConnection = conn
        runtimeStopped = false

        conn.stateUpdateHandler = { [weak self] (state: NWConnection.State) in
            guard let self else { return }
            guard self.isActiveConnection(connID) else { return }
            JVPNDebugLog.tunnel("NWConnection state=\(Self.describeNWState(state))")
            switch state {
            case .ready:
                self.reconnectAttempt = 0
                self.ioQueue.async {
                    self.runSession(conn: conn, connID: connID, token: config.token, completionHandler: completionHandler)
                }
            case .failed(let err):
                JVPNDebugLog.tunnel("NWConnection failed: \(String(describing: err))")
                if self.startCompleted {
                    self.scheduleReconnectIfNeeded(reason: err)
                } else {
                    self.finishStart(err, completionHandler)
                }
            case .cancelled:
                // Intentional cancel during reconnect already armed a retry — don't nest another.
                self.startLock.lock()
                let alreadyArming = self.reconnectScheduled || self.runtimeStopped
                self.startLock.unlock()
                if self.startCompleted && !alreadyArming {
                    let err = NSError(domain: "JVPN", code: 8, userInfo: [NSLocalizedDescriptionKey: "Cancelled"])
                    self.scheduleReconnectIfNeeded(reason: err)
                } else if !self.startCompleted {
                    let err = NSError(domain: "JVPN", code: 8, userInfo: [NSLocalizedDescriptionKey: "Cancelled"])
                    self.finishStart(err, completionHandler)
                }
            case .setup, .waiting, .preparing:
                break
            @unknown default:
                break
            }
        }
        conn.start(queue: ioQueue)
    }

    private func scheduleReconnectIfNeeded(reason: Error) {
        startLock.lock()
        let shouldReconnect = startCompleted && !isClosing && !runtimeStopped && !reconnectScheduled
        if shouldReconnect {
            runtimeStopped = true
            reconnectScheduled = true
        }
        startLock.unlock()
        guard shouldReconnect, let cfg = backendConfig else { return }

        cancelHeartbeatAndTelemetry()
        resetSendQueue(cancelPending: true)
        vpnConnection?.cancel()
        vpnConnection = nil
        reconnectWorkItem?.cancel()
        reconnectAttempt += 1
        // Prefer sticky transport; flip only after sustained failures (not every few attempts).
        stickyTransportFailures += 1
        if stickyTransportFailures >= transportFlipAfterFailures, transportCandidates.count > 1 {
            transportIndex = (transportIndex + 1) % transportCandidates.count
            stickyTransportFailures = 0
            JVPNDebugLog.tunnel("transport failover -> \(transportCandidates[transportIndex]) after sustained failures")
        }

        let delay = Self.reconnectDelaySeconds(attempt: reconnectAttempt)
        JVPNDebugLog.tunnel("reconnect scheduled in \(String(format: "%.2f", delay))s (\(reason.localizedDescription))")
        let work = DispatchWorkItem { [weak self] in
            guard let self else { return }
            self.startLock.lock()
            let canReconnect = self.startCompleted && !self.isClosing
            self.runtimeStopped = false
            self.reconnectScheduled = false
            self.startLock.unlock()
            guard canReconnect else { return }
            self.connectBackend(config: cfg, isInitial: false, completionHandler: { _ in })
        }
        reconnectWorkItem = work
        ioQueue.asyncAfter(deadline: .now() + delay, execute: work)
    }

    /// Fast retry after drops (speedtest / path loss); then exponential up to 15s.
    private static func reconnectDelaySeconds(attempt: Int) -> TimeInterval {
        let cappedAttempt = max(1, attempt)
        if cappedAttempt <= 4 {
            return 0.2 + Double.random(in: 0...0.3)
        }
        let base = min(15.0, pow(2.0, Double(cappedAttempt - 4)))
        let jitter = base * Double.random(in: 0...0.25)
        return base + jitter
    }

    private func runSession(conn: NWConnection, connID: UInt64, token: String, completionHandler: @escaping (Error?) -> Void) {
        guard let cfg = backendConfig else {
            failDuringBringUp(NSError(domain: "JVPN", code: 21, userInfo: [NSLocalizedDescriptionKey: "Missing backend config"]), completionHandler)
            return
        }
        sendHandshake(conn: conn, token: token, host: cfg.host, port: cfg.port) { [weak self] err in
            guard let self else { return }
            guard self.isActiveConnection(connID) else { return }
            if let err {
                self.failDuringBringUp(err, completionHandler)
                return
            }
            self.readHandshakeResponse(conn: conn, host: cfg.host, port: cfg.port) { [weak self] result in
                guard let self else { return }
                guard self.isActiveConnection(connID) else { return }
                switch result {
                case .failure(let err):
                    self.failDuringBringUp(err, completionHandler)
                case .success(let clientIP, let prefixLen):
                    self.stickyTransportFailures = 0
                    Self.savePreferredTransport(self.activeTransport, host: cfg.host, port: cfg.port)
                    self.applySettingsAndBridge(conn: conn, connID: connID, clientIP: clientIP, prefixLen: prefixLen, completionHandler: completionHandler)
                }
            }
        }
    }

    private func applySettingsAndBridge(conn: NWConnection, connID: UInt64, clientIP: String, prefixLen: Int, completionHandler: @escaping (Error?) -> Void) {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "10.8.0.1")
        let ipv4 = NEIPv4Settings(addresses: [clientIP], subnetMasks: [Self.netmask(forPrefixLength: prefixLen)])
        ipv4.includedRoutes = [NEIPv4Route.default()]
        settings.ipv4Settings = ipv4
        settings.mtu = NSNumber(value: 1400)
        let dns = NEDNSSettings(servers: ["1.1.1.1", "8.8.8.8"])
        dns.matchDomains = [""]
        settings.dnsSettings = dns

        let beginBridging: () -> Void = { [weak self] in
            guard let self else { return }
            guard self.isActiveConnection(connID) else { return }
            self.startLock.lock()
            self.lastAppliedClientIP = clientIP
            self.lastAppliedPrefixLen = prefixLen
            self.startLock.unlock()
            self.recvLoop(conn: conn, connID: connID)
            self.readPacketsLoop(conn: conn, connID: connID)
            self.sendTelemetryIfNeeded(conn: conn, connID: connID, force: true)
            self.scheduleHeartbeat(conn: conn, connID: connID)
            self.scheduleTelemetryPoll(conn: conn, connID: connID)
            self.finishStart(nil, completionHandler)
        }

        startLock.lock()
        let tunnelAlreadyUp = startCompleted && lastAppliedClientIP == clientIP && lastAppliedPrefixLen == prefixLen
        startLock.unlock()

        if tunnelAlreadyUp {
            beginBridging()
            return
        }

        setTunnelNetworkSettings(settings) { [weak self] error in
            guard let self else { return }
            guard self.isActiveConnection(connID) else { return }
            if let error {
                self.failDuringBringUp(error, completionHandler)
                return
            }
            beginBridging()
        }
    }

    private func scheduleHeartbeat(conn: NWConnection, connID: UInt64) {
        heartbeatWorkItem?.cancel()
        let work = DispatchWorkItem { [weak self] in
            guard let self else { return }
            guard self.isActiveConnection(connID), !self.isClosing else { return }
            self.sendRaw(conn: conn, data: JVPNControlProtocol.heartbeatFrame()) { [weak self] err in
                guard let self else { return }
                guard self.isActiveConnection(connID) else { return }
                if let err {
                    JVPNDebugLog.tunnel("heartbeat send: \(String(describing: err))")
                    self.scheduleReconnectIfNeeded(reason: err)
                    return
                }
                self.scheduleHeartbeat(conn: conn, connID: connID)
            }
        }
        heartbeatWorkItem = work
        ioQueue.asyncAfter(deadline: .now() + JVPNControlProtocol.heartbeatInterval, execute: work)
    }

    private func scheduleTelemetryPoll(conn: NWConnection, connID: UInt64) {
        telemetryPollWorkItem?.cancel()
        let work = DispatchWorkItem { [weak self] in
            guard let self else { return }
            guard self.isActiveConnection(connID), !self.isClosing else { return }
            self.sendTelemetryIfNeeded(conn: conn, connID: connID, force: false)
            self.scheduleTelemetryPoll(conn: conn, connID: connID)
        }
        telemetryPollWorkItem = work
        ioQueue.asyncAfter(deadline: .now() + JVPNControlProtocol.telemetryPollInterval, execute: work)
    }

    private func sendTelemetryIfNeeded(conn: NWConnection, connID: UInt64, force: Bool) {
        guard isActiveConnection(connID), !isClosing else { return }
        let rev = JVPNAppGroupTelemetry.currentRevision()
        if !force, rev == lastSentTelemetryRevision { return }
        var meta = JVPNAppGroupTelemetry.snapshotDictionary(clientIDFallback: Self.stableClientID())
        if meta["device_name"] == nil {
            meta["device_name"] = ProcessInfo.processInfo.hostName
        }
        if meta["model"] == nil {
            meta["model"] = Self.machineIdentifier()
        }
        if meta["os"] == nil {
            meta["os"] = ProcessInfo.processInfo.operatingSystemVersionString
        }
        if meta["updated_at"] == nil {
            meta["updated_at"] = String(Int(Date().timeIntervalSince1970))
        }
        guard let framed = JVPNControlProtocol.telemetryFrame(jsonObject: meta) else { return }
        sendRaw(conn: conn, data: framed) { [weak self] err in
            guard let self else { return }
            guard self.isActiveConnection(connID) else { return }
            if let err {
                JVPNDebugLog.tunnel("telemetry send: \(String(describing: err))")
                return
            }
            self.lastSentTelemetryRevision = rev
            JVPNDebugLog.tunnel("telemetry frame sent revision=\(rev)")
        }
    }

    private func cancelHeartbeatAndTelemetry() {
        heartbeatWorkItem?.cancel()
        heartbeatWorkItem = nil
        telemetryPollWorkItem?.cancel()
        telemetryPollWorkItem = nil
    }

    private func recvLoop(conn: NWConnection, connID: UInt64) {
        if isClosing { return }
        receiveExact(conn: conn, count: 4) { [weak self] header, err in
            guard let self else { return }
            guard self.isActiveConnection(connID) else { return }
            if let err {
                if !self.isClosing {
                    JVPNDebugLog.tunnel("receive header: \(String(describing: err))")
                    self.scheduleReconnectIfNeeded(reason: err)
                }
                return
            }
            guard let header, header.count == 4 else {
                if !self.isClosing {
                    self.scheduleReconnectIfNeeded(reason: NSError(domain: "JVPN", code: 11, userInfo: [NSLocalizedDescriptionKey: "Short header"]))
                }
                return
            }
            let len = UInt32(header[0]) << 24 | UInt32(header[1]) << 16 | UInt32(header[2]) << 8 | UInt32(header[3])
            if len == 0 || len > 65535 {
                if !self.isClosing {
                    self.scheduleReconnectIfNeeded(reason: NSError(domain: "JVPN", code: 12, userInfo: [NSLocalizedDescriptionKey: "Invalid frame length"]))
                }
                return
            }
            self.receiveExact(conn: conn, count: Int(len)) { [weak self] payload, err in
                guard let self else { return }
                guard self.isActiveConnection(connID) else { return }
                if let err {
                    if !self.isClosing {
                        JVPNDebugLog.tunnel("receive body: \(String(describing: err))")
                        self.scheduleReconnectIfNeeded(reason: err)
                    }
                    return
                }
                guard let payload else {
                    if !self.isClosing {
                        self.scheduleReconnectIfNeeded(reason: NSError(domain: "JVPN", code: 13, userInfo: [NSLocalizedDescriptionKey: "Empty payload"]))
                    }
                    return
                }
                if !JVPNControlProtocol.isControlPayload(payload) {
                    self.packetFlow.writePackets([payload], withProtocols: [NSNumber(value: AF_INET as Int32)])
                }
                self.recvLoop(conn: conn, connID: connID)
            }
        }
    }

    private func readPacketsLoop(conn: NWConnection, connID: UInt64) {
        if isClosing { return }
        packetFlow.readPackets { [weak self] packets, protocols in
            guard let self else { return }
            guard self.isActiveConnection(connID) else { return }
            if self.isClosing { return }
            var v4: [Data] = []
            for (i, packet) in packets.enumerated() {
                let p = i < protocols.count ? protocols[i] : NSNumber(value: AF_INET as Int32)
                if p.intValue == AF_INET { v4.append(packet) }
            }
            self.sendBatch(conn: conn, connID: connID, packets: v4, index: 0)
        }
    }

    private func sendBatch(conn: NWConnection, connID: UInt64, packets: [Data], index: Int) {
        if isClosing { return }
        if index >= packets.count {
            readPacketsLoop(conn: conn, connID: connID)
            return
        }
        var out = Data()
        // Keep WS messages small — large batched frames drop under speedtest load.
        let batchLimit = useWebSocket ? 8 * 1024 : 64 * 1024
        out.reserveCapacity(min(batchLimit, 64 * 1024))
        var i = index
        while i < packets.count {
            let framed = Self.frame(packets[i])
            if !out.isEmpty && out.count + framed.count > batchLimit {
                break
            }
            // WebSocket: at most a few packets per message to limit latency spikes.
            if useWebSocket && !out.isEmpty && i - index >= 4 {
                break
            }
            out.append(framed)
            i += 1
        }
        sendRaw(conn: conn, data: out) { [weak self] err in
            guard let self else { return }
            guard self.isActiveConnection(connID) else { return }
            if let err {
                if !self.isClosing {
                    JVPNDebugLog.tunnel("send frame: \(String(describing: err))")
                    self.scheduleReconnectIfNeeded(reason: err)
                }
                return
            }
            self.sendBatch(conn: conn, connID: connID, packets: packets, index: i)
        }
    }

    private func sendHandshake(conn: NWConnection, token: String, host: String, port: UInt16, done: @escaping (Error?) -> Void) {
        var data = Data([0x4A, 0x56, 0x50, 0x4E, 0x03])
        let t = Data(token.utf8)
        guard t.count <= 4096 else {
            done(NSError(domain: "JVPN", code: 2, userInfo: [NSLocalizedDescriptionKey: "Token too long"]))
            return
        }
        let n = UInt16(t.count)
        data.append(UInt8(n >> 8))
        data.append(UInt8(n & 0xff))
        data.append(t)
        let meta = Self.deviceMetadataData()
        var metaMap = (try? JSONSerialization.jsonObject(with: meta)) as? [String: String] ?? [:]
        metaMap["transport_used"] = activeTransport
        let finalMeta = (try? JSONSerialization.data(withJSONObject: metaMap)) ?? meta
        let mn = UInt16(min(finalMeta.count, 4096))
        data.append(UInt8(mn >> 8))
        data.append(UInt8(mn & 0xff))
        if mn > 0 {
            data.append(finalMeta.prefix(Int(mn)))
        }
        let resume = Self.loadResumeToken(host: host, port: port)
        let rb = Data(resume.utf8)
        let rn = UInt16(min(rb.count, 512))
        data.append(UInt8(rn >> 8))
        data.append(UInt8(rn & 0xff))
        if rn > 0 {
            data.append(rb.prefix(Int(rn)))
        }
        sendRaw(conn: conn, data: data, done: done)
    }

    private func readHandshakeResponse(conn: NWConnection, host: String, port: UInt16, done: @escaping (Result<(String, Int), Error>) -> Void) {
        receiveExact(conn: conn, count: 1) { [weak self] data, error in
            guard let self else { return }
            if let error { done(.failure(error)); return }
            guard let data, let status = data.first else {
                done(.failure(NSError(domain: "JVPN", code: 4, userInfo: [NSLocalizedDescriptionKey: "Empty handshake"])))
                return
            }
            if status != 0 {
                done(.failure(NSError(domain: "JVPN", code: 5, userInfo: [NSLocalizedDescriptionKey: "Authentication denied"])))
                return
            }
            self.receiveExact(conn: conn, count: 5) { rest, error in
                if let error { done(.failure(error)); return }
                guard let rest, rest.count == 5 else {
                    done(.failure(NSError(domain: "JVPN", code: 7, userInfo: [NSLocalizedDescriptionKey: "Bad handshake body"])))
                    return
                }
                let ip = "\(rest[0]).\(rest[1]).\(rest[2]).\(rest[3])"
                let prefix = Int(rest[4])
                self.receiveExact(conn: conn, count: 2) { tokLenBytes, _ in
                    guard let tokLenBytes, tokLenBytes.count == 2 else {
                        done(.success((ip, prefix)))
                        return
                    }
                    let n = Int(tokLenBytes[0]) << 8 | Int(tokLenBytes[1])
                    if n <= 0 || n > 512 {
                        done(.success((ip, prefix)))
                        return
                    }
                    self.receiveExact(conn: conn, count: n) { tokBytes, _ in
                        if let tokBytes, let tok = String(data: tokBytes, encoding: .utf8), !tok.isEmpty {
                            Self.saveResumeToken(tok, host: host, port: port)
                        }
                        done(.success((ip, prefix)))
                    }
                }
            }
        }
    }

    private func resetSendQueue(cancelPending: Bool) {
        let pending = sendQueue
        sendQueue.removeAll(keepingCapacity: true)
        sendInFlight = false
        guard cancelPending else { return }
        let err = NSError(domain: "JVPN", code: 8, userInfo: [NSLocalizedDescriptionKey: "Send cancelled"])
        for item in pending {
            item.done(err)
        }
    }

    private func sendRaw(conn: NWConnection, data: Data, done: @escaping (Error?) -> Void) {
        sendQueue.append((conn, data, done))
        pumpSendQueue()
    }

    private func pumpSendQueue() {
        guard !sendInFlight, !sendQueue.isEmpty else { return }
        let item = sendQueue[0]
        sendInFlight = true

        let finish: (Error?) -> Void = { [weak self] err in
            guard let self else { return }
            // NWCompletion may arrive off our queue.
            self.ioQueue.async {
                if !self.sendQueue.isEmpty {
                    self.sendQueue.removeFirst()
                }
                self.sendInFlight = false
                item.done(err)
                if let err {
                    let rest = self.sendQueue
                    self.sendQueue.removeAll(keepingCapacity: true)
                    for pending in rest {
                        pending.done(err)
                    }
                    return
                }
                self.pumpSendQueue()
            }
        }

        if useWebSocket {
            let meta = NWProtocolWebSocket.Metadata(opcode: .binary)
            let ctx = NWConnection.ContentContext(identifier: "jvpn-ws", metadata: [meta])
            item.conn.send(content: item.data, contentContext: ctx, isComplete: true, completion: .contentProcessed(finish))
        } else {
            item.conn.send(content: item.data, completion: .contentProcessed(finish))
        }
    }

    private func receiveExact(conn: NWConnection, count: Int, done: @escaping (Data?, Error?) -> Void) {
        if !useWebSocket {
            conn.receive(minimumIncompleteLength: count, maximumLength: count) { data, _, isComplete, error in
                if let error { done(nil, error); return }
                if isComplete {
                    done(nil, NSError(domain: "JVPN", code: 3, userInfo: [NSLocalizedDescriptionKey: "Connection closed"]))
                    return
                }
                done(data, nil)
            }
            return
        }
        fillWSBuffer(conn: conn, minBytes: count) { [weak self] error in
            guard let self else { return }
            if let error { done(nil, error); return }
            if self.wsRecvBuffer.count < count {
                done(nil, NSError(domain: "JVPN", code: 3, userInfo: [NSLocalizedDescriptionKey: "Connection closed"]))
                return
            }
            let out = self.wsRecvBuffer.prefix(count)
            self.wsRecvBuffer.removeFirst(count)
            done(Data(out), nil)
        }
    }

    private func fillWSBuffer(conn: NWConnection, minBytes: Int, done: @escaping (Error?) -> Void) {
        if wsRecvBuffer.count >= minBytes { done(nil); return }
        conn.receiveMessage { [weak self] data, context, isComplete, error in
            guard let self else { return }
            if let error { done(error); return }
            if !isComplete {
                done(NSError(domain: "JVPN", code: 9, userInfo: [NSLocalizedDescriptionKey: "websocket partial message"]))
                return
            }
            guard let data else {
                done(NSError(domain: "JVPN", code: 3, userInfo: [NSLocalizedDescriptionKey: "Connection closed"]))
                return
            }
            let metadata = context?.protocolMetadata(definition: NWProtocolWebSocket.definition) as? NWProtocolWebSocket.Metadata
            if metadata?.opcode == .binary {
                self.wsRecvBuffer.append(data)
            }
            self.fillWSBuffer(conn: conn, minBytes: minBytes, done: done)
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        isClosing = true
        startLock.lock()
        currentConnectionID &+= 1
        reconnectScheduled = false
        startCompleted = false
        lastAppliedClientIP = nil
        lastAppliedPrefixLen = nil
        startLock.unlock()
        cancelHeartbeatAndTelemetry()
        resetSendQueue(cancelPending: true)
        reconnectWorkItem?.cancel()
        reconnectWorkItem = nil
        vpnConnection?.cancel()
        vpnConnection = nil
        completionHandler()
    }

    /// After the tunnel is up, `finishStart` only runs once; handshake/settings failures must reconnect instead of no-op.
    private func failDuringBringUp(_ error: Error, _ completionHandler: @escaping (Error?) -> Void) {
        startLock.lock()
        let alreadyUp = startCompleted
        startLock.unlock()
        if alreadyUp {
            // Mid-session bring-up failure: stay sticky; only flip after sustained failures.
            scheduleReconnectIfNeeded(reason: error)
        } else {
            finishStart(error, completionHandler)
        }
    }

    private func finishStart(_ error: Error?, _ completionHandler: @escaping (Error?) -> Void) {
        startLock.lock()
        defer { startLock.unlock() }
        guard !startCompleted else { return }
        startCompleted = true
        if let error {
            JVPNDebugLog.tunnel("startTunnel failed: \(error.localizedDescription)")
        } else {
            JVPNDebugLog.tunnel("startTunnel completed successfully")
        }
        completionHandler(error)
    }

    private func isActiveConnection(_ id: UInt64) -> Bool {
        startLock.lock()
        defer { startLock.unlock() }
        return id == currentConnectionID && !isClosing
    }

    private static func normalizeSharedToken(_ raw: String) -> String {
        let t = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return "" }
        for u in t.unicodeScalars {
            let v = u.value
            let isHex = (v >= 48 && v <= 57) || (v >= 65 && v <= 70) || (v >= 97 && v <= 102)
            if !isHex { return t }
        }
        return t.lowercased()
    }

    private static func normalizedWSPath(_ raw: String) -> String {
        var p = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if p.isEmpty { p = "/ws" }
        if !p.hasPrefix("/") { p = "/" + p }
        return p
    }

    private static func resolveTransportCandidates(_ mode: String, preferred: String?) -> [String] {
        let base: [String]
        switch mode {
        case "tcp":
            base = ["tcp", "ws"]
        case "auto":
            base = ["ws", "tcp"]
        case "ws":
            base = ["ws", "tcp"]
        default:
            base = ["ws", "tcp"]
        }
        guard let preferred, (preferred == "ws" || preferred == "tcp"), base.contains(preferred) else {
            return base
        }
        var out = [preferred]
        for t in base where t != preferred {
            out.append(t)
        }
        return out
    }

    private static func machineIdentifier() -> String {
        var u = utsname()
        uname(&u)
        return withUnsafePointer(to: &u.machine) {
            $0.withMemoryRebound(to: CChar.self, capacity: Int(_SYS_NAMELEN)) { String(cString: $0) }
        }
    }

    private static func deviceMetadataData() -> Data {
        var meta = JVPNAppGroupTelemetry.snapshotDictionary(clientIDFallback: stableClientID())
        if meta["device_name"] == nil {
            meta["device_name"] = ProcessInfo.processInfo.hostName
        }
        if meta["model"] == nil {
            meta["model"] = machineIdentifier()
        }
        if meta["os"] == nil {
            meta["os"] = ProcessInfo.processInfo.operatingSystemVersionString
        }
        meta["app_bundle_id"] = Bundle.main.bundleIdentifier ?? "unknown"
        return (try? JSONSerialization.data(withJSONObject: meta)) ?? Data()
    }

    private static func stableClientID() -> String {
        JVPNAppGroupTelemetry.ensureClientID()
    }

    private static func resumeTokenKey(host: String, port: UInt16) -> String {
        "org.jackh54.jvpn.resume.\(host.lowercased()):\(port)"
    }

    private static func loadResumeToken(host: String, port: UInt16) -> String {
        UserDefaults.standard.string(forKey: resumeTokenKey(host: host, port: port)) ?? ""
    }

    private static func saveResumeToken(_ token: String, host: String, port: UInt16) {
        UserDefaults.standard.set(token, forKey: resumeTokenKey(host: host, port: port))
    }

    private static func preferredTransportKey(host: String, port: UInt16) -> String {
        "org.jackh54.jvpn.preferred_transport.\(host.lowercased()):\(port)"
    }

    private static func loadPreferredTransport(host: String, port: UInt16) -> String? {
        UserDefaults.standard.string(forKey: preferredTransportKey(host: host, port: port))
    }

    private static func savePreferredTransport(_ transport: String, host: String, port: UInt16) {
        guard transport == "ws" || transport == "tcp" else { return }
        UserDefaults.standard.set(transport, forKey: preferredTransportKey(host: host, port: port))
    }

    private static func describeNWState(_ state: NWConnection.State) -> String {
        switch state {
        case .setup: return "setup"
        case .waiting(let err): return "waiting(\(String(describing: err)))"
        case .preparing: return "preparing"
        case .ready: return "ready"
        case .failed(let err): return "failed(\(String(describing: err)))"
        case .cancelled: return "cancelled"
        @unknown default: return "unknown"
        }
    }

    private static func frame(_ payload: Data) -> Data {
        JVPNControlProtocol.frame(payload)
    }

    private static func netmask(forPrefixLength len: Int) -> String {
        guard len >= 0 && len <= 32 else { return "255.255.255.0" }
        var mask: UInt32 = 0
        if len > 0 {
            mask = ~UInt32(0) << UInt32(32 - len)
        }
        return "\(UInt8((mask >> 24) & 0xff)).\(UInt8((mask >> 16) & 0xff)).\(UInt8((mask >> 8) & 0xff)).\(UInt8(mask & 0xff))"
    }
}
