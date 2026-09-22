//
//  JVPNServiceConfig.swift
//  JVPN
//
//  Committed defaults use placeholders for the shared token.
//  For local/dev secrets, copy Configs/Secrets.local.xcconfig.example →
//  Configs/Secrets.local.xcconfig (gitignored) and set JVPN_SHARED_TOKEN.
//  Host/port can also be overridden there; defaults keep vpn.blakout.dev:443.
//

import Foundation

enum JVPNServiceConfig {
    static let serverHost: String = {
        if let override = stringSetting("JVPNServerHost"), !override.isEmpty {
            return override
        }
        return "vpn.blakout.dev"
    }()

    static let serverPort: UInt16 = {
        if let override = stringSetting("JVPNServerPort"),
           let port = UInt16(override), port > 0 {
            return port
        }
        return 443
    }()

    /// Pre-shared token (must match server `-token-file`). Never commit a real production token.
    static let sharedToken: String = {
        if let override = stringSetting("JVPNSharedToken"), !override.isEmpty {
            return override
        }
        return "REPLACE_WITH_YOUR_SERVER_TOKEN"
    }()

    /// When `true`, the packet tunnel does **not** verify the server TLS certificate.
    /// Set **`false`** when the server uses a public CA (e.g. Let’s Encrypt).
    static let acceptSelfSignedTLS = false

    /// The only supported transport. WebSocket upgrades are blocked on the
    /// networks JVPN has to cross, so every tunnel runs UDP-over-TCP on 443.
    static let transport = "uot"

    /// Retained so existing tunnel profiles keep a stable providerConfiguration
    /// shape; unused while `transport == "uot"`.
    static let webSocketPath = "/ws"

    /// HTTP path for the UDP-over-TCP (DoH-style POST) tunnel on TLS 443.
    static let uotPath = "/dns-query"

    static var isPlaceholderConfiguration: Bool {
        sharedToken == "REPLACE_WITH_YOUR_SERVER_TOKEN" || sharedToken.isEmpty
    }

    private static func stringSetting(_ key: String) -> String? {
        Bundle.main.object(forInfoDictionaryKey: key) as? String
    }
}
