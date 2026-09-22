//
//  JVPNScheduleManager.swift
//  JVPN
//
//  Applies the admin-authored schedule on the device.
//
//  Three things enforce it, because no single one covers every state:
//    1. On-demand rules keep the tunnel up once it is connected, so "always on"
//       needs no timer at all.
//    2. The packet tunnel arms its own off-timer and refuses to start inside an
//       off window, which covers auto-off while the app is not running.
//    3. This manager reconciles whenever the app is awake, and schedules the
//       repeating local notification for the daily turn-on.
//

import Combine
import Foundation
import NetworkExtension
import UserNotifications

#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

@MainActor
final class JVPNScheduleManager: ObservableObject {
    static let shared = JVPNScheduleManager()

    @Published private(set) var policy: JVPNSchedulePolicy
    /// True while the tunnel is down because the schedule turned it off.
    @Published private(set) var isSuspendedBySchedule = false

    private static let onNotificationPrefix = "jvpn.schedule.on."
    private static let tickInterval: Duration = .seconds(30)
    private static let lastAutoConnectKey = "schedule.last_auto_connect"

    private var ticker: Task<Void, Never>?
    private var foregroundObserver: NSObjectProtocol?
    private var scheduledNotificationSignature: String?
    private var started = false

    private init() {
        policy = JVPNAppGroupTelemetry.schedulePolicy()
    }

    // MARK: - Lifecycle

    func start() {
        guard !started else { return }
        started = true
        observeForeground()
        ticker = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: Self.tickInterval)
                guard !Task.isCancelled else { return }
                await self?.refresh()
            }
        }
        Task { await refresh() }
    }

    private func observeForeground() {
#if canImport(UIKit)
        let name = UIApplication.didBecomeActiveNotification
#elseif canImport(AppKit)
        let name = NSApplication.didBecomeActiveNotification
#else
        return
#endif
#if canImport(UIKit) || canImport(AppKit)
        foregroundObserver = NotificationCenter.default.addObserver(
            forName: name,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in
                await self?.refresh()
            }
        }
#endif
    }

    // MARK: - Reconciliation

    /// Re-reads the policy the tunnel cached, re-arms notifications, and brings
    /// the tunnel in line with what the schedule wants right now.
    func refresh() async {
        let latest = JVPNAppGroupTelemetry.schedulePolicy()
        if latest != policy {
            policy = latest
            JVPNDebugLog.app("schedule policy updated (revision \(latest.revision))")
        }
        isSuspendedBySchedule = JVPNAppGroupTelemetry.scheduleSuspendedUntil() != nil
        syncNotifications()
        await applyIntent()
    }

    private func applyIntent() async {
        let now = Date()
        let vpn = VPNManager.shared
        switch policy.intent(at: now) {
        case .disconnect:
            guard Self.isLive(vpn.status) else { return }
            guard JVPNAppGroupTelemetry.scheduleManualOverrideUntil() == nil else { return }
            JVPNDebugLog.app("schedule: turning the VPN off for the off window")
            vpn.disconnectForSchedule(resumeAt: policy.nextOnDate)
            notifyScheduledOff(resumeAt: policy.nextOnDate)
        case .connect:
            guard !Self.isLive(vpn.status) else {
                markAutoConnectHandled(now)
                return
            }
            // Never raise the system VPN-permission prompt on the schedule's
            // behalf; the first connect stays a deliberate user action.
            guard vpn.isConfigurationInstalled else { return }
            // Act once per on-time occurrence so a manual disconnect sticks until
            // the next scheduled turn-on.
            guard let occurrence = policy.previousOccurrence(of: policy.onTime, before: now),
                  !hasHandledAutoConnect(occurrence)
            else { return }
            markAutoConnectHandled(occurrence)
            JVPNDebugLog.app("schedule: turning the VPN on for \(policy.localizedTime(policy.onTime))")
            do {
                try await vpn.connect(userInitiated: false)
            } catch {
                JVPNDebugLog.app("schedule connect failed: \(error.localizedDescription)")
            }
        case .none:
            break
        }
    }

    private static func isLive(_ status: NEVPNStatus) -> Bool {
        switch status {
        case .connected, .connecting, .reasserting:
            return true
        default:
            return false
        }
    }

    private func hasHandledAutoConnect(_ occurrence: Date) -> Bool {
        let stored = JVPNAppGroupTelemetry.defaults?.double(forKey: Self.lastAutoConnectKey) ?? 0
        return stored >= occurrence.timeIntervalSince1970
    }

    private func markAutoConnectHandled(_ occurrence: Date) {
        JVPNAppGroupTelemetry.defaults?.set(occurrence.timeIntervalSince1970, forKey: Self.lastAutoConnectKey)
    }

    // MARK: - Notifications

    /// The turn-on announcement has to fire when the app is not running, so it is
    /// a repeating calendar notification rather than an in-process timer. The
    /// turn-off announcement is posted by the tunnel at the moment it disconnects,
    /// so it only ever fires when something actually happened.
    private func syncNotifications() {
        let signature = notificationSignature()
        guard signature != scheduledNotificationSignature else { return }
        scheduledNotificationSignature = signature

        let center = UNUserNotificationCenter.current()
        // Identifiers are deterministic (one per weekday at most), so they can be
        // cleared outright instead of querying the pending list asynchronously.
        center.removePendingNotificationRequests(
            withIdentifiers: (0..<7).map { "\(Self.onNotificationPrefix)\($0)" }
        )

        guard policy.autoConnect, policy.notifyOn, JVPNAppGroupTelemetry.notificationsEnabled() else { return }

        let content = UNMutableNotificationContent()
        content.title = "JVPN"
        content.body = "Scheduled VPN time — JVPN turns on now."
        content.sound = .default

        for (index, components) in policy.triggerComponents(for: policy.onTime).enumerated() {
            let trigger = UNCalendarNotificationTrigger(dateMatching: components, repeats: true)
            let request = UNNotificationRequest(
                identifier: "\(Self.onNotificationPrefix)\(index)",
                content: content,
                trigger: trigger
            )
            center.add(request)
        }
    }

    private func notificationSignature() -> String {
        let days = (policy.days ?? []).sorted().map(String.init).joined(separator: ",")
        return [
            policy.onTime,
            policy.timezone,
            days,
            String(policy.autoConnect),
            String(policy.notifyOn),
            String(JVPNAppGroupTelemetry.notificationsEnabled()),
        ].joined(separator: "|")
    }

    private func notifyScheduledOff(resumeAt: Date?) {
        guard policy.notifyOff else { return }
        var body = "VPN turned off on schedule."
        if let resumeAt {
            let fmt = DateFormatter()
            fmt.dateStyle = .none
            fmt.timeStyle = .short
            body = "VPN turned off on schedule. It turns back on at \(fmt.string(from: resumeAt))."
        }
        VPNNotificationManager.post(title: "JVPN", body: body, id: "jvpn.vpn-schedule")
    }

    // MARK: - Display

    var summaryLine: String {
        guard policy.autoConnect || policy.autoDisconnect else {
            return "No schedule — JVPN stays on until you turn it off."
        }
        var parts: [String] = []
        if policy.autoConnect {
            parts.append("On at \(policy.localizedTime(policy.onTime))")
        }
        if policy.autoDisconnect {
            parts.append("off at \(policy.localizedTime(policy.offTime))")
        } else {
            parts.append("stays on until you turn it off")
        }
        return parts.joined(separator: ", ") + "."
    }

    var daysLine: String {
        guard let days = policy.days, !days.isEmpty, days.count < 7 else { return "Every day" }
        let names = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
        return days.sorted().compactMap { names.indices.contains($0) ? names[$0] : nil }.joined(separator: " · ")
    }
}
