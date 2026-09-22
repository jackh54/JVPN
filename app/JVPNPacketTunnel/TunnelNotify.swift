//
//  TunnelNotify.swift
//  JVPNPacketTunnel
//

import Foundation
import UserNotifications

enum TunnelNotify {
    private static let statusID = "jvpn.vpn-status"
    private static let scheduleID = "jvpn.vpn-schedule"

    static func connected() {
        post(title: "JVPN", body: "VPN is connected and protecting your traffic.")
    }

    static func reconnecting() {
        post(title: "JVPN", body: "VPN is reconnecting. Your connection will stay on.")
    }

    /// Fired by the packet tunnel when the admin schedule turns the VPN off.
    static func scheduledOff(resumeAt: Date?) {
        var body = "VPN turned off on schedule."
        if let resumeAt {
            let fmt = DateFormatter()
            fmt.dateStyle = .none
            fmt.timeStyle = .short
            body = "VPN turned off on schedule. It turns back on at \(fmt.string(from: resumeAt))."
        }
        post(title: "JVPN", body: body, id: scheduleID)
    }

    static func post(title: String, body: String, id: String = statusID) {
        guard JVPNAppGroupTelemetry.notificationsEnabled() else { return }
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default
        let req = UNNotificationRequest(identifier: id, content: content, trigger: nil)
        UNUserNotificationCenter.current().add(req)
    }
}
