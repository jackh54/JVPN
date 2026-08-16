//
//  VPNManager.swift
//  JVPN
//

import Combine
import Foundation
import NetworkExtension

enum VPNManagerError: LocalizedError {
    case noConfiguration

    var errorDescription: String? {
        switch self {
        case .noConfiguration:
            return "VPN configuration is not available."
        }
    }
}

@MainActor
final class VPNManager: ObservableObject {
    static let shared = VPNManager()

    @Published private(set) var status: NEVPNStatus = .invalid
    @Published private(set) var lastError: String?

    // Resolved at runtime by scanning the embedded packet-tunnel extension's bundle ID.
    // This is robust across iOS, Mac Catalyst (which can rename app bundle IDs), and macOS.
    private lazy var tunnelProviderIdentifier: String = {
        Self.discoverPacketTunnelProviderIdentifier() ?? "org.jackh54.JVPN.JVPNPacketTunnel"
    }()

    private static func discoverPacketTunnelProviderIdentifier() -> String? {
        guard let pluginsURL = Bundle.main.builtInPlugInsURL,
              let entries = try? FileManager.default.contentsOfDirectory(at: pluginsURL, includingPropertiesForKeys: nil)
        else { return nil }
        for url in entries where url.pathExtension == "appex" {
            guard let bundle = Bundle(url: url),
                  let info = bundle.infoDictionary,
                  let ext = info["NSExtension"] as? [String: Any],
                  let pointID = ext["NSExtensionPointIdentifier"] as? String,
                  pointID == "com.apple.networkextension.packet-tunnel",
                  let id = bundle.bundleIdentifier
            else { continue }
            return id
        }
        return nil
    }

    private var manager: NETunnelProviderManager?
    private var statusObserver: NSObjectProtocol?
    private var lastObservedStatus: NEVPNStatus = .invalid
    private var isInstallingConfiguration = false

    private init() {}

    private static var runtimePlatformTag: String {
#if os(macOS)
        return "macos"
#elseif os(iOS)
        return "ios"
#else
        return "unknown"
#endif
    }

    func load() async {
        JVPNDebugLog.app("VPNManager.load() begin providerID=\(tunnelProviderIdentifier)")
        do {
            let managers = try await NETunnelProviderManager.loadAllFromPreferences()
            for m in managers {
                let id = (m.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier
                if id != tunnelProviderIdentifier || m.protocolConfiguration as? NETunnelProviderProtocol == nil {
                    JVPNDebugLog.app("Removing stale VPN profile providerID=\(id ?? "<nil>")")
                    try? await m.removeFromPreferences()
                }
            }
            let refreshed = try await NETunnelProviderManager.loadAllFromPreferences()
            manager = refreshed.first { m in
                (m.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == tunnelProviderIdentifier
            } ?? NETunnelProviderManager()
            bindStatus()
            JVPNDebugLog.app("VPNManager.load() ok, status=\(Self.neStatusLabel(status))")
        } catch {
            lastError = error.localizedDescription
            JVPNDebugLog.app("VPNManager.load() failed: \(error.localizedDescription)")
        }
        if Self.discoverPacketTunnelProviderIdentifier() == nil {
            let msg = "Embedded packet tunnel extension not found in app bundle. Verify the JVPNPacketTunnel target is embedded for this platform."
            lastError = msg
            JVPNDebugLog.app(msg)
        }
    }

    /// Saves the VPN profile to the system. On first success, iOS shows **“JVPN” Would Like to Add VPN Configurations** (or similar). Call after `load()` so the prompt appears without requiring a button tap first.
    func registerConfigurationWithSystem() async {
        lastError = nil
        JVPNDebugLog.app("registerConfigurationWithSystem() begin")
        do {
            try await installConfigurationIfNeeded()
            JVPNDebugLog.app("registerConfigurationWithSystem() saved preferences")
        } catch {
            lastError = error.localizedDescription
            JVPNDebugLog.app("registerConfigurationWithSystem() failed: \(error.localizedDescription)")
        }
    }

    /// Writes the built-in server host, port, and token from `JVPNServiceConfig`.
    func installConfigurationIfNeeded() async throws {
        guard !isInstallingConfiguration else {
            // Avoid overlapping saves/loads that can invalidate temporary IDs in nesessionmanager.
            return
        }
        isInstallingConfiguration = true
        defer { isInstallingConfiguration = false }

        let m = manager ?? NETunnelProviderManager()
        let mode = JVPNExperimentalSettings.shared.connectionMode
        let transport = mode.tunnelTransport
        let providerConfiguration: [String: NSObject] = [
            "host": JVPNServiceConfig.serverHost as NSString,
            "port": NSNumber(value: JVPNServiceConfig.serverPort),
            "token": JVPNServiceConfig.sharedToken as NSString,
            "acceptInsecureTLS": NSNumber(value: JVPNServiceConfig.acceptSelfSignedTLS),
            "transport": transport as NSString,
            "wsPath": JVPNServiceConfig.webSocketPath as NSString,
            "uotPath": JVPNServiceConfig.uotPath as NSString,
            "platform": Self.runtimePlatformTag as NSString,
        ]
        let existingProto = m.protocolConfiguration as? NETunnelProviderProtocol
        let configMatches =
            existingProto?.providerBundleIdentifier == tunnelProviderIdentifier &&
            existingProto?.serverAddress == JVPNServiceConfig.serverHost &&
            NSDictionary(dictionary: existingProto?.providerConfiguration ?? [:]).isEqual(to: providerConfiguration)
        let expectedName = mode == .udpOverTCP ? "JVPN Experimental" : "JVPN"
        let needsAlwaysOn =
            existingProto == nil ||
            !(existingProto?.includeAllNetworks ?? false) ||
            !(existingProto?.excludeLocalNetworks ?? false) ||
            (existingProto?.disconnectOnSleep ?? true)
        let shouldSave = !configMatches || !m.isEnabled || needsAlwaysOn || m.localizedDescription != expectedName

        if !shouldSave {
            manager = m
            bindStatus()
            JVPNDebugLog.app("installConfiguration skipped (already up to date)")
            return
        }

        let proto = existingProto ?? NETunnelProviderProtocol()
        applyAlwaysOnProtocol(proto, providerConfiguration: providerConfiguration)
        m.protocolConfiguration = proto
        m.localizedDescription = expectedName
        m.isEnabled = true
        JVPNDebugLog.app(
            "installConfiguration host=\(JVPNServiceConfig.serverHost) port=\(JVPNServiceConfig.serverPort) tokenLen=\(JVPNServiceConfig.sharedToken.count) acceptInsecureTLS=\(JVPNServiceConfig.acceptSelfSignedTLS) transport=\(transport) wsPath=\(JVPNServiceConfig.webSocketPath) uotPath=\(JVPNServiceConfig.uotPath)"
        )
        do {
            try await m.saveToPreferences()
        } catch {
            proto.includeAllNetworks = false
            m.protocolConfiguration = proto
            try await m.saveToPreferences()
            JVPNDebugLog.app("installConfiguration saved without includeAllNetworks: \(error.localizedDescription)")
        }
        manager = try await reloadCurrentManagerFromPreferences()
        JVPNDebugLog.app("installConfiguration saveToPreferences done")
    }

    private func applyAlwaysOnProtocol(_ proto: NETunnelProviderProtocol, providerConfiguration: [String: NSObject]) {
        proto.providerBundleIdentifier = tunnelProviderIdentifier
        proto.serverAddress = JVPNServiceConfig.serverHost
        proto.providerConfiguration = providerConfiguration
        proto.disconnectOnSleep = false
        proto.includeAllNetworks = true
        proto.excludeLocalNetworks = true
        if #available(iOS 16.0, macOS 13.0, *) {
            proto.excludeAPNs = true
        }
    }

    private func setOnDemandEnabled(_ enabled: Bool) async throws {
        let m = try await reloadCurrentManagerFromPreferences()
        m.isEnabled = true
        if enabled {
            let rule = NEOnDemandRuleConnect()
            rule.interfaceTypeMatch = .any
            m.onDemandRules = [rule]
            m.isOnDemandEnabled = true
        } else {
            m.isOnDemandEnabled = false
            m.onDemandRules = []
        }
        try await m.saveToPreferences()
        manager = try await reloadCurrentManagerFromPreferences()
    }

    func connect() async throws {
        lastError = nil
        JVPNDebugLog.app("connect() begin")
        try await installConfigurationIfNeeded()
        guard manager != nil else {
            JVPNDebugLog.app("connect() abort: no manager")
            throw VPNManagerError.noConfiguration
        }
        try await setOnDemandEnabled(true)
        let m = try await reloadCurrentManagerFromPreferences()
        guard m.connection as? NETunnelProviderSession != nil else {
            JVPNDebugLog.app("connect() abort: connection is not NETunnelProviderSession")
            throw VPNManagerError.noConfiguration
        }
        switch m.connection.status {
        case .connected, .connecting, .reasserting:
            JVPNDebugLog.app("connect() already active status=\(Self.neStatusLabel(m.connection.status))")
            return
        default:
            break
        }
        try m.connection.startVPNTunnel()
        JVPNDebugLog.app("connect() startVPNTunnel() returned; status=\(Self.neStatusLabel(m.connection.status))")
    }

    func disconnect() {
        lastError = nil
        JVPNDebugLog.app("disconnect() begin; disabling on-demand then stopping tunnel")
        Task { @MainActor in
            guard let m = manager else { return }
            do {
                try await m.loadFromPreferences()
                m.isOnDemandEnabled = false
                m.onDemandRules = []
                try await m.saveToPreferences()
                JVPNDebugLog.app("disconnect() on-demand disabled")
            } catch {
                // Even if prefs save fails, still request disconnect.
                JVPNDebugLog.app("disconnect() failed to disable on-demand: \(error.localizedDescription)")
            }
            m.connection.stopVPNTunnel()
            JVPNDebugLog.app("disconnect() stopVPNTunnel sent")
        }
    }

    private func bindStatus() {
        guard let m = manager else { return }
        let initial = m.connection.status
        lastObservedStatus = initial
        if status != initial {
            status = initial
        }
        JVPNDebugLog.app("bindStatus initial=\(Self.neStatusLabel(initial))")
        if let statusObserver {
            NotificationCenter.default.removeObserver(statusObserver)
        }
        statusObserver = NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange, object: m.connection, queue: .main) { [weak self] _ in
            Task { @MainActor in
                guard let self else { return }
                let previous = self.lastObservedStatus
                let current = m.connection.status
                JVPNDebugLog.app("NEVPNStatusDidChange \(Self.neStatusLabel(previous)) -> \(Self.neStatusLabel(current))")
                self.lastObservedStatus = current
                if current != self.status {
                    self.status = current
                }
                if previous != current {
                    switch current {
                    case .connected, .reasserting:
                        VPNNotificationManager.notifyStatus(current)
                    case .disconnected, .invalid:
                        if previous == .connected || previous == .reasserting || previous == .disconnecting {
                            VPNNotificationManager.notifyStatus(.disconnected)
                        }
                    default:
                        break
                    }
                }
                self.reportTransitionIfFailed(previous: previous, current: current)
            }
        }
    }

    private func reloadCurrentManagerFromPreferences() async throws -> NETunnelProviderManager {
        let all = try await NETunnelProviderManager.loadAllFromPreferences()
        guard let refreshed = all.first(where: {
            ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == tunnelProviderIdentifier
        }) else {
            throw VPNManagerError.noConfiguration
        }
        manager = refreshed
        bindStatus()
        return refreshed
    }

    private func reportTransitionIfFailed(previous: NEVPNStatus, current: NEVPNStatus) {
        guard previous == .connecting, current == .disconnected else { return }
        let msg = "Tunnel extension failed to launch. macOS rejected the Network Extension load — verify Apple Developer team membership, code signing, and provisioning profile cover the JVPNPacketTunnel target."
        lastError = msg
        JVPNDebugLog.app(msg)
    }

    private static func neStatusLabel(_ s: NEVPNStatus) -> String {
        switch s {
        case .invalid: return "invalid"
        case .disconnected: return "disconnected"
        case .connecting: return "connecting"
        case .connected: return "connected"
        case .reasserting: return "reasserting"
        case .disconnecting: return "disconnecting"
        @unknown default: return "unknown"
        }
    }

}
