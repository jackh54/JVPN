//
//  DeviceIdentity.swift
//  JVPN
//
//  Keep in sync with JVPNPacketTunnel/DeviceIdentity.swift.
//

import Darwin
import Foundation

#if canImport(UIKit)
import UIKit
#endif

enum DeviceIdentity {
    /// User-visible device name from Settings → General → About → Name.
    static var userVisibleName: String {
#if canImport(UIKit)
        let name = UIDevice.current.name.trimmingCharacters(in: .whitespacesAndNewlines)
        if !name.isEmpty { return name }
#endif
        let host = ProcessInfo.processInfo.hostName.trimmingCharacters(in: .whitespacesAndNewlines)
        return host.isEmpty ? "Unknown Device" : host
    }

    static var hardwareModelIdentifier: String {
        var u = utsname()
        uname(&u)
        return withUnsafePointer(to: &u.machine) {
            $0.withMemoryRebound(to: CChar.self, capacity: Int(_SYS_NAMELEN)) { String(cString: $0) }
        }
    }

    static var friendlyModelName: String {
        friendlyName(for: hardwareModelIdentifier)
    }

    static var shortOSVersion: String {
#if os(iOS)
        let v = ProcessInfo.processInfo.operatingSystemVersion
        return "iOS \(v.majorVersion).\(v.minorVersion)"
#elseif os(macOS)
        let v = ProcessInfo.processInfo.operatingSystemVersion
        return "macOS \(v.majorVersion).\(v.minorVersion)"
#else
        return ProcessInfo.processInfo.operatingSystemVersionString
#endif
    }

    static func friendlyName(for identifier: String) -> String {
        modelNames[identifier] ?? identifier
    }

    private static let modelNames: [String: String] = [
        "iPhone14,4": "iPhone 13 mini",
        "iPhone14,5": "iPhone 13",
        "iPhone14,2": "iPhone 13 Pro",
        "iPhone14,3": "iPhone 13 Pro Max",
        "iPhone14,7": "iPhone 14",
        "iPhone14,8": "iPhone 14 Plus",
        "iPhone15,2": "iPhone 14 Pro",
        "iPhone15,3": "iPhone 14 Pro Max",
        "iPhone15,4": "iPhone 15",
        "iPhone15,5": "iPhone 15 Plus",
        "iPhone16,1": "iPhone 15 Pro",
        "iPhone16,2": "iPhone 15 Pro Max",
        "iPhone17,3": "iPhone 16",
        "iPhone17,4": "iPhone 16 Plus",
        "iPhone17,1": "iPhone 16 Pro",
        "iPhone17,2": "iPhone 16 Pro Max",
        "iPhone12,1": "iPhone 11",
        "iPhone12,3": "iPhone 11 Pro",
        "iPhone12,5": "iPhone 11 Pro Max",
        "iPhone13,1": "iPhone 12 mini",
        "iPhone13,2": "iPhone 12",
        "iPhone13,3": "iPhone 12 Pro",
        "iPhone13,4": "iPhone 12 Pro Max",
        "iPad13,18": "iPad (10th gen)",
        "iPad13,19": "iPad (10th gen)",
        "iPad14,3": "iPad Pro 11\" (4th gen)",
        "iPad14,4": "iPad Pro 11\" (4th gen)",
        "iPad14,5": "iPad Pro 12.9\" (6th gen)",
        "iPad14,6": "iPad Pro 12.9\" (6th gen)",
        "iPad16,3": "iPad Pro 11\" (M4)",
        "iPad16,4": "iPad Pro 11\" (M4)",
        "iPad16,5": "iPad Pro 13\" (M4)",
        "iPad16,6": "iPad Pro 13\" (M4)",
    ]
}
