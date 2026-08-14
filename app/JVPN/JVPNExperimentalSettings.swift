//
//  JVPNExperimentalSettings.swift
//  JVPN
//
//  Opt-in connection modes stored in the App Group so the packet tunnel
//  profile is rebuilt with the selected transport on the next connect.
//

import Combine
import Foundation

enum JVPNConnectionMode: String, CaseIterable, Identifiable {
    case standard
    case udpOverTCP

    var id: String { rawValue }

    var tunnelTransport: String {
        switch self {
        case .standard:
            return JVPNServiceConfig.transport
        case .udpOverTCP:
            return "uot"
        }
    }

    var title: String {
        switch self {
        case .standard:
            return "Standard"
        case .udpOverTCP:
            return "UDP-over-TCP 443"
        }
    }

    var subtitle: String {
        switch self {
        case .standard:
            return "WebSocket over TLS 443, with TCP fallback."
        case .udpOverTCP:
            return "WireGuard-style obfuscation: UDP datagrams tunneled over TCP 443."
        }
    }
}

@MainActor
final class JVPNExperimentalSettings: ObservableObject {
    static let shared = JVPNExperimentalSettings()

    static let suiteName = JVPNAppGroupTelemetry.suiteName
    static let modeKey = "experimental.connection_mode"

    @Published var connectionMode: JVPNConnectionMode {
        didSet { persist(connectionMode) }
    }

    private init() {
        connectionMode = Self.loadMode()
    }

    var isExperimentalTransport: Bool {
        connectionMode == .udpOverTCP
    }

    private func persist(_ mode: JVPNConnectionMode) {
        UserDefaults(suiteName: Self.suiteName)?.set(mode.rawValue, forKey: Self.modeKey)
        UserDefaults.standard.set(mode.rawValue, forKey: Self.modeKey)
    }

    private static func loadMode() -> JVPNConnectionMode {
        let raw = UserDefaults(suiteName: suiteName)?.string(forKey: modeKey)
            ?? UserDefaults.standard.string(forKey: modeKey)
            ?? ""
        return JVPNConnectionMode(rawValue: raw) ?? .standard
    }
}
