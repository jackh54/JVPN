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

    /// `auto` = try websocket first and fail over to tcp, `ws` = websocket only, `tcp` = tcp first with ws fallback.
    static let transport = "auto"

    /// Websocket upgrade path used when `transport == "ws"` / auto.
    static let webSocketPath = "/ws"

    static var isPlaceholderConfiguration: Bool {
        sharedToken == "REPLACE_WITH_YOUR_SERVER_TOKEN" || sharedToken.isEmpty
    }

    private static func stringSetting(_ key: String) -> String? {
        Bundle.main.object(forInfoDictionaryKey: key) as? String
    }
}
