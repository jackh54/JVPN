//
//  TunnelNotify.swift
//  JVPNPacketTunnel
//

import Foundation
import UserNotifications

enum TunnelNotify {
    private static let statusID = "jvpn.vpn-status"

    static func connected() {
        post(title: "JVPN", body: "VPN is connected and protecting your traffic.")
    }

    static func reconnecting() {
        post(title: "JVPN", body: "VPN is reconnecting. Your connection will stay on.")
    }

    static func post(title: String, body: String) {
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default
        let req = UNNotificationRequest(identifier: statusID, content: content, trigger: nil)
        UNUserNotificationCenter.current().add(req)
    }
}
