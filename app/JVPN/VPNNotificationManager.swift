//
//  VPNNotificationManager.swift
//  JVPN
//

import Foundation
import UserNotifications
import NetworkExtension

enum VPNNotificationManager {
    private static let statusID = "jvpn.vpn-status"

    static func requestAuthorization() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { _, _ in }
    }

    static func notifyStatus(_ status: NEVPNStatus) {
        switch status {
        case .connected:
            post(title: "JVPN", body: "VPN is connected and protecting your traffic.")
        case .reasserting:
            post(title: "JVPN", body: "VPN is reconnecting. Your connection will stay on.")
        case .disconnected, .invalid:
            post(title: "JVPN", body: "VPN is disconnected.")
        default:
            break
        }
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
