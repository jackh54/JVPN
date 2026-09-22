//
//  JVPNExperimentalSettings.swift
//  JVPN
//
//  User-facing switches stored in the App Group so the packet tunnel reads the
//  same values. The transport is no longer selectable: WebSocket upgrades stop
//  working on the networks JVPN has to cross, so every tunnel runs UDP-over-TCP
//  on 443 (see JVPNServiceConfig.transport).
//

import Combine
import Foundation

@MainActor
final class JVPNExperimentalSettings: ObservableObject {
    static let shared = JVPNExperimentalSettings()

    static let suiteName = JVPNAppGroupTelemetry.suiteName
    /// Legacy key from when the transport was user-selectable; cleared on launch.
    private static let legacyModeKey = "experimental.connection_mode"

    @Published var notificationsEnabled: Bool {
        didSet { persistNotifications(notificationsEnabled) }
    }

    private init() {
        notificationsEnabled = Self.loadNotificationsEnabled()
        Self.clearLegacyTransportPreference()
    }

    /// Human-readable name of the one transport the app uses.
    var transportTitle: String { "UDP-over-TCP 443" }

    var transportSubtitle: String {
        "UDP datagrams tunneled over TLS 443 with DNS-over-HTTPS camouflage (POST \(JVPNServiceConfig.uotPath))."
    }

    private func persistNotifications(_ enabled: Bool) {
        UserDefaults(suiteName: Self.suiteName)?.set(enabled, forKey: JVPNAppGroupTelemetry.Key.notificationsEnabled)
        UserDefaults.standard.set(enabled, forKey: JVPNAppGroupTelemetry.Key.notificationsEnabled)
    }

    private static func loadNotificationsEnabled() -> Bool {
        JVPNAppGroupTelemetry.notificationsEnabled()
    }

    private static func clearLegacyTransportPreference() {
        UserDefaults(suiteName: suiteName)?.removeObject(forKey: legacyModeKey)
        UserDefaults.standard.removeObject(forKey: legacyModeKey)
    }
}
