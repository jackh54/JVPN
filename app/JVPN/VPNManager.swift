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
    /// Enable on-demand only after a successful connect; enabling earlier causes a reconnect storm on failure.
    private var enableOnDemandAfterConnect = false
    /// When set, disable on-demand once status reaches `.disconnected` (never save prefs mid-transition).
    private var disableOnDemandWhenIdle = false
    private var isSavingPreferences = false
    private var didReportCurrentFailure = false

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

            if let m = manager {
                let storming = m.connection.status == .connecting || m.connection.status == .disconnecting
                if m.isOnDemandEnabled && storming {
                    JVPNDebugLog.app("load() stopping reconnect storm")
                    disableOnDemandWhenIdle = true
                    m.connection.stopVPNTunnel()
                }
            }
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
        let expectedName = "JVPN"
        let wantIncludeAll = Self.preferIncludeAllNetworks
        let needsProtocolFlags =
            existingProto == nil ||
            (existingProto?.includeAllNetworks ?? false) != wantIncludeAll ||
            !(existingProto?.excludeLocalNetworks ?? false) ||
            (existingProto?.disconnectOnSleep ?? true)
        // Never leave on-demand armed across an install — it races startVPNTunnel.
        let shouldSave =
            !configMatches || !m.isEnabled || needsProtocolFlags || m.localizedDescription != expectedName
            || m.isOnDemandEnabled || !(m.onDemandRules?.isEmpty ?? true)

        if !shouldSave {
            manager = m
            bindStatus()
            JVPNDebugLog.app("installConfiguration skipped (already up to date)")
            return
        }

        let proto = existingProto ?? NETunnelProviderProtocol()
        applyTunnelProtocol(proto, providerConfiguration: providerConfiguration)
        m.protocolConfiguration = proto
        m.localizedDescription = expectedName
        m.isEnabled = true
        m.isOnDemandEnabled = false
        m.onDemandRules = []
        JVPNDebugLog.app(
            "installConfiguration host=\(JVPNServiceConfig.serverHost) port=\(JVPNServiceConfig.serverPort) tokenLen=\(JVPNServiceConfig.sharedToken.count) acceptInsecureTLS=\(JVPNServiceConfig.acceptSelfSignedTLS) transport=\(transport) includeAllNetworks=\(wantIncludeAll) wsPath=\(JVPNServiceConfig.webSocketPath) uotPath=\(JVPNServiceConfig.uotPath)"
        )
        try await savePreferences(m)
        manager = try await reloadCurrentManagerFromPreferences()
        JVPNDebugLog.app("installConfiguration saveToPreferences done")
    }

    private static var preferIncludeAllNetworks: Bool {
#if os(macOS)
        // includeAllNetworks on macOS frequently fails tunnel bring-up and then flaps
        // connecting ↔ disconnecting when prefs are saved during the transition.
        return false
#else
        return true
#endif
    }

    private func applyTunnelProtocol(_ proto: NETunnelProviderProtocol, providerConfiguration: [String: NSObject]) {
        proto.providerBundleIdentifier = tunnelProviderIdentifier
        proto.serverAddress = JVPNServiceConfig.serverHost
        proto.providerConfiguration = providerConfiguration
        proto.disconnectOnSleep = false
        proto.includeAllNetworks = Self.preferIncludeAllNetworks
        proto.excludeLocalNetworks = true
        if #available(iOS 16.0, macOS 13.0, *) {
            proto.excludeAPNs = true
        }
    }

    private func savePreferences(_ m: NETunnelProviderManager) async throws {
        isSavingPreferences = true
        defer { isSavingPreferences = false }
        do {
            try await m.saveToPreferences()
        } catch {
            if let proto = m.protocolConfiguration as? NETunnelProviderProtocol, proto.includeAllNetworks {
                proto.includeAllNetworks = false
                m.protocolConfiguration = proto
                try await m.saveToPreferences()
                JVPNDebugLog.app("saveToPreferences without includeAllNetworks: \(error.localizedDescription)")
                return
            }
            throw error
        }
    }

    private func setOnDemandEnabled(_ enabled: Bool) async throws {
        let m = try await reloadCurrentManagerFromPreferences()
        let already =
            m.isOnDemandEnabled == enabled
            && (enabled ? !(m.onDemandRules?.isEmpty ?? true) : (m.onDemandRules?.isEmpty ?? true))
        if already, m.isEnabled {
            return
        }
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
        try await savePreferences(m)
        manager = try await reloadCurrentManagerFromPreferences()
    }

    func connect() async throws {
        lastError = nil
        didReportCurrentFailure = false
        disableOnDemandWhenIdle = false
        enableOnDemandAfterConnect = true
        JVPNDebugLog.app("connect() begin")
        try await installConfigurationIfNeeded()
        guard let m = manager else {
            enableOnDemandAfterConnect = false
            JVPNDebugLog.app("connect() abort: no manager")
            throw VPNManagerError.noConfiguration
        }
        // Install already cleared on-demand. Avoid another prefs save before start —
        // saving while the session starts is what retriggers connecting↔disconnecting.
        guard m.connection as? NETunnelProviderSession != nil else {
            enableOnDemandAfterConnect = false
            JVPNDebugLog.app("connect() abort: connection is not NETunnelProviderSession")
            throw VPNManagerError.noConfiguration
        }
        switch m.connection.status {
        case .connected:
            JVPNDebugLog.app("connect() already connected")
            try? await setOnDemandEnabled(true)
            enableOnDemandAfterConnect = false
            return
        case .connecting, .reasserting:
            JVPNDebugLog.app("connect() already in progress status=\(Self.neStatusLabel(m.connection.status))")
            return
        case .disconnecting:
            JVPNDebugLog.app("connect() waiting for disconnect to finish before restart")
            m.connection.stopVPNTunnel()
            return
        default:
            break
        }
        try m.connection.startVPNTunnel()
        JVPNDebugLog.app("connect() startVPNTunnel() returned; status=\(Self.neStatusLabel(m.connection.status))")
    }

    func disconnect() {
        lastError = nil
        enableOnDemandAfterConnect = false
        didReportCurrentFailure = false
        disableOnDemandWhenIdle = true
        JVPNDebugLog.app("disconnect() begin; stop tunnel then disable on-demand when idle")
        manager?.connection.stopVPNTunnel()
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
        statusObserver = NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange, object: m.connection, queue: .main) { [weak self] note in
            Task { @MainActor in
                guard let self else { return }
                // Ignore stale notifications from a previous manager instance.
                guard let currentManager = self.manager, note.object as AnyObject? === currentManager.connection as AnyObject? else {
                    return
                }
                let previous = self.lastObservedStatus
                let current = currentManager.connection.status
                if previous == current, current == self.status { return }
                JVPNDebugLog.app("NEVPNStatusDidChange \(Self.neStatusLabel(previous)) -> \(Self.neStatusLabel(current))")
                self.lastObservedStatus = current
                if current != self.status {
                    self.status = current
                }
                self.handleStatusTransition(previous: previous, current: current)
            }
        }
    }

    private func handleStatusTransition(previous: NEVPNStatus, current: NEVPNStatus) {
        switch current {
        case .connected, .reasserting:
            didReportCurrentFailure = false
            VPNNotificationManager.notifyStatus(current)
            if current == .connected, enableOnDemandAfterConnect, !isSavingPreferences {
                enableOnDemandAfterConnect = false
                Task { @MainActor in
                    do {
                        try await setOnDemandEnabled(true)
                        JVPNDebugLog.app("on-demand enabled after successful connect")
                    } catch {
                        JVPNDebugLog.app("failed to enable on-demand: \(error.localizedDescription)")
                    }
                }
            }
        case .disconnected, .invalid:
            if previous == .connected || previous == .reasserting || previous == .disconnecting {
                VPNNotificationManager.notifyStatus(.disconnected)
            }
            if disableOnDemandWhenIdle {
                disableOnDemandWhenIdle = false
                Task { @MainActor in
                    do {
                        try await setOnDemandEnabled(false)
                        JVPNDebugLog.app("on-demand disabled after idle")
                    } catch {
                        JVPNDebugLog.app("failed to disable on-demand after idle: \(error.localizedDescription)")
                    }
                }
            }
        default:
            break
        }

        let failedStart =
            previous == .connecting && (current == .disconnecting || current == .disconnected)
        guard failedStart, !didReportCurrentFailure else { return }
        didReportCurrentFailure = true
        enableOnDemandAfterConnect = false
        // Stop first; only touch preferences once we are idle to avoid restart loops.
        disableOnDemandWhenIdle = true
        manager?.connection.stopVPNTunnel()

        let transport = JVPNExperimentalSettings.shared.connectionMode.title
        let msg = "VPN failed to start (\(transport)). The Mac tunnel plugin was rejected — rebuild from Xcode and try Connect again."
        lastError = msg
        JVPNDebugLog.app(msg)
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
