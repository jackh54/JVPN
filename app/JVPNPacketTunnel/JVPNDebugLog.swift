//
//  JVPNDebugLog.swift
//  JVPNPacketTunnel
//
//  Keep in sync with JVPN/JVPNDebugLog.swift (same subsystem for Console.app filters).
//

import Foundation
import os

/// Debug-only; release avoids string work and logging on hot paths (battery). Never log tokens.
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
