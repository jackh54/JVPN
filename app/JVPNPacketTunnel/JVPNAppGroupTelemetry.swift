//
//  JVPNAppGroupTelemetry.swift
//  JVPNPacketTunnel
//
//  App Group bridge for battery/GPS telemetry (main app → packet tunnel).
//  Keep in sync with JVPN/JVPNAppGroupTelemetry.swift.
//

import Foundation

enum JVPNAppGroupTelemetry {
    static let suiteName = "group.org.jackh54.JVPN"

    enum Key {
        static let clientID = "telemetry.client_id"
        static let deviceName = "telemetry.device_name"
        static let model = "telemetry.model"
        static let os = "telemetry.os"
        static let batteryPct = "telemetry.battery_pct"
        static let charging = "telemetry.charging"
        static let lat = "telemetry.lat"
        static let lon = "telemetry.lon"
        static let updatedAt = "telemetry.updated_at"
        static let revision = "telemetry.revision"
    }

    static var defaults: UserDefaults? {
        UserDefaults(suiteName: suiteName)
    }

    static func ensureClientID() -> String {
        let key = Key.clientID
        if let suite = defaults, let existing = suite.string(forKey: key), !existing.isEmpty {
            return existing
        }
        if let existing = UserDefaults.standard.string(forKey: key), !existing.isEmpty {
            defaults?.set(existing, forKey: key)
            return existing
        }
        let id = UUID().uuidString.lowercased()
        defaults?.set(id, forKey: key)
        UserDefaults.standard.set(id, forKey: key)
        return id
    }

    static func write(
        deviceName: String,
        model: String,
        os: String,
        batteryPct: String?,
        charging: String?,
        lat: String?,
        lon: String?
    ) {
        guard let d = defaults else { return }
        let clientID = ensureClientID()
        d.set(clientID, forKey: Key.clientID)
        d.set(deviceName, forKey: Key.deviceName)
        d.set(model, forKey: Key.model)
        d.set(os, forKey: Key.os)
        if let batteryPct { d.set(batteryPct, forKey: Key.batteryPct) }
        if let charging { d.set(charging, forKey: Key.charging) }
        if let lat { d.set(lat, forKey: Key.lat) }
        if let lon { d.set(lon, forKey: Key.lon) }
        d.set(String(Int(Date().timeIntervalSince1970)), forKey: Key.updatedAt)
        let rev = d.integer(forKey: Key.revision) &+ 1
        d.set(rev, forKey: Key.revision)
    }

    /// Snapshot for handshake metadata + control telemetry frames.
    static func snapshotDictionary(clientIDFallback: String) -> [String: String] {
        let d = defaults
        var out: [String: String] = [:]
        let clientID = d?.string(forKey: Key.clientID).flatMap { $0.isEmpty ? nil : $0 } ?? clientIDFallback
        out["client_id"] = clientID
        if let v = d?.string(forKey: Key.deviceName), !v.isEmpty { out["device_name"] = v }
        if let v = d?.string(forKey: Key.model), !v.isEmpty { out["model"] = v }
        if let v = d?.string(forKey: Key.os), !v.isEmpty { out["os"] = v }
        if let v = d?.string(forKey: Key.batteryPct), !v.isEmpty { out["battery_pct"] = v }
        if let v = d?.string(forKey: Key.charging), !v.isEmpty { out["charging"] = v }
        if let v = d?.string(forKey: Key.lat), !v.isEmpty { out["lat"] = v }
        if let v = d?.string(forKey: Key.lon), !v.isEmpty { out["lon"] = v }
        if let v = d?.string(forKey: Key.updatedAt), !v.isEmpty { out["updated_at"] = v }
        return out
    }

    static func currentRevision() -> Int {
        defaults?.integer(forKey: Key.revision) ?? 0
    }
}
