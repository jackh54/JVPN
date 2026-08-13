//
//  JVPNControlProtocol.swift
//  JVPN
//
//  Post-handshake framed control messages (length-prefixed like IP frames).
//  Keep in sync with JVPNPacketTunnel/JVPNControlProtocol.swift.
//

import Foundation

/// Control frames are never valid IPv4 (version nibble would be 0xC).
enum JVPNControlProtocol {
    /// First payload byte — invalid as IPv4 version.
    static let magic: UInt8 = 0xC0
    static let typeTelemetry: UInt8 = 0x01
    static let typeHeartbeat: UInt8 = 0x02

    static let heartbeatInterval: TimeInterval = 27
    static let telemetryPollInterval: TimeInterval = 12

    static func isControlPayload(_ payload: Data) -> Bool {
        guard let first = payload.first else { return false }
        return first == magic
    }

    static func heartbeatFrame() -> Data {
        frame(Data([magic, typeHeartbeat]))
    }

    /// Telemetry JSON keys: client_id, device_name, model, os, battery_pct, charging, lat, lon, updated_at.
    static func telemetryFrame(jsonObject: [String: String]) -> Data? {
        guard let json = try? JSONSerialization.data(withJSONObject: jsonObject),
              json.count + 2 <= 65535
        else { return nil }
        var payload = Data([magic, typeTelemetry])
        payload.append(json)
        return frame(payload)
    }

    static func frame(_ payload: Data) -> Data {
        let c = UInt32(payload.count)
        var out = Data(count: 4)
        out[0] = UInt8((c >> 24) & 0xff)
        out[1] = UInt8((c >> 16) & 0xff)
        out[2] = UInt8((c >> 8) & 0xff)
        out[3] = UInt8(c & 0xff)
        out.append(payload)
        return out
    }
}
