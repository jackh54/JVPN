//
//  JVPNDebugLog.swift
//  JVPN
//
//  Keep in sync with JVPNPacketTunnel/JVPNDebugLog.swift (same subsystem for Console.app filters).
//

import Foundation
import os

/// Debug-only logging (subsystem **org.jackh54.JVPN**). Release builds avoid string formatting and log I/O for battery/perf. Never log tokens.
enum JVPNDebugLog {
    private static let appLogger = Logger(subsystem: "org.jackh54.JVPN", category: "App")
    private static let tunnelLogger = Logger(subsystem: "org.jackh54.JVPN", category: "PacketTunnel")

    static func app(_ message: @autoclosure () -> String) {
#if DEBUG
        let text = message()
        appLogger.debug("\(text, privacy: .public)")
#endif
    }

    static func tunnel(_ message: @autoclosure () -> String) {
#if DEBUG
        let text = message()
        tunnelLogger.debug("\(text, privacy: .public)")
#endif
    }
}
