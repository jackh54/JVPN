//
//  SettingsView.swift
//  JVPN
//

import NetworkExtension
import SwiftUI

struct SettingsView: View {
    @ObservedObject private var settings = JVPNExperimentalSettings.shared
    @ObservedObject private var schedule = JVPNScheduleManager.shared
    @ObservedObject private var vpn = VPNManager.shared
    @Environment(\.dismiss) private var dismiss

    private let accent = Color(red: 0.24, green: 0.87, blue: 0.60)
    private let surfaceElevated = Color(red: 0.15, green: 0.17, blue: 0.21)

    var body: some View {
        ZStack {
            Color(red: 0.06, green: 0.07, blue: 0.09)
                .ignoresSafeArea()

            VStack(alignment: .leading, spacing: 0) {
                header
                    .padding(.horizontal, 24)
                    .padding(.top, 20)
                    .padding(.bottom, 28)

                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        sectionTitle("Schedule")
                        scheduleCard

                        sectionTitle("Connection")
                        transportCard

                        sectionTitle("Notifications")
                        notificationsRow
                    }
                    .padding(.horizontal, 24)
                    .padding(.bottom, 36)
                }
            }
        }
        .preferredColorScheme(.dark)
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Settings")
                    .font(.system(size: 22, weight: .bold, design: .rounded))
                    .foregroundStyle(.white)
                Text("Schedule and transport are managed from the admin dashboard.")
                    .font(.system(size: 13, weight: .regular, design: .rounded))
                    .foregroundStyle(Color.white.opacity(0.45))
            }
            Spacer()
            Text("Done")
                .font(.system(size: 15, weight: .semibold, design: .rounded))
                .foregroundStyle(accent)
                .contentShape(Rectangle())
                .onTapGesture { dismiss() }
                .accessibilityAddTraits(.isButton)
                .accessibilityLabel("Done")
        }
    }

    private func sectionTitle(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 12, weight: .semibold, design: .rounded))
            .foregroundStyle(Color.white.opacity(0.4))
            .textCase(.uppercase)
            .tracking(0.8)
            .padding(.horizontal, 4)
            .padding(.top, 8)
    }

    // MARK: - Schedule

    private var scheduleCard: some View {
        VStack(alignment: .leading, spacing: 14) {
            scheduleRow(
                icon: "sunrise.fill",
                title: "Turns on",
                value: schedule.policy.autoConnect
                    ? schedule.policy.localizedTime(schedule.policy.onTime)
                    : "Not scheduled",
                active: schedule.policy.autoConnect
            )
            scheduleRow(
                icon: "moon.fill",
                title: "Turns off",
                value: schedule.policy.autoDisconnect
                    ? schedule.policy.localizedTime(schedule.policy.offTime)
                    : "Never — stays on",
                active: schedule.policy.autoDisconnect
            )
            scheduleRow(
                icon: "calendar",
                title: "Days",
                value: schedule.daysLine,
                active: true
            )
            scheduleRow(
                icon: "globe",
                title: "Time zone",
                value: schedule.policy.timezone,
                active: true
            )

            if let next = nextTransitionText {
                Text(next)
                    .font(.system(size: 12.5, weight: .regular, design: .rounded))
                    .foregroundStyle(accent.opacity(0.85))
                    .fixedSize(horizontal: false, vertical: true)
            }

            Text("Set these in the JVPN admin dashboard; the server pushes changes to this device while it is connected.")
                .font(.system(size: 12.5, weight: .regular, design: .rounded))
                .foregroundStyle(Color.white.opacity(0.4))
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(cardBackground)
    }

    private var nextTransitionText: String? {
        let fmt = DateFormatter()
        fmt.dateStyle = .none
        fmt.timeStyle = .short
        if schedule.isSuspendedBySchedule, let on = schedule.policy.nextOnDate {
            return "Off on schedule — turns back on at \(fmt.string(from: on))."
        }
        if let off = schedule.policy.nextOffDate, isTunnelActive {
            return "Turns off at \(fmt.string(from: off))."
        }
        if let on = schedule.policy.nextOnDate, !isTunnelActive {
            return "Turns on at \(fmt.string(from: on))."
        }
        return nil
    }

    private func scheduleRow(icon: String, title: String, value: String, active: Bool) -> some View {
        HStack(alignment: .center, spacing: 12) {
            Image(systemName: icon)
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(active ? accent.opacity(0.85) : Color.white.opacity(0.3))
                .frame(width: 20)
            Text(title)
                .font(.system(size: 14, weight: .medium, design: .rounded))
                .foregroundStyle(Color.white.opacity(0.6))
            Spacer(minLength: 12)
            Text(value)
                .font(.system(size: 14, weight: .semibold, design: .rounded))
                .foregroundStyle(active ? .white : Color.white.opacity(0.45))
                .multilineTextAlignment(.trailing)
        }
        .accessibilityElement(children: .combine)
    }

    // MARK: - Connection

    private var transportCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Text(settings.transportTitle)
                    .font(.system(size: 16, weight: .semibold, design: .rounded))
                    .foregroundStyle(.white)
                Text("ONLY MODE")
                    .font(.system(size: 10, weight: .bold, design: .rounded))
                    .tracking(0.6)
                    .foregroundStyle(accent)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 3)
                    .background(Capsule().fill(accent.opacity(0.14)))
            }
            Text(settings.transportSubtitle)
                .font(.system(size: 13, weight: .regular, design: .rounded))
                .foregroundStyle(Color.white.opacity(0.48))
                .fixedSize(horizontal: false, vertical: true)
            Text("WebSocket transport was removed — those upgrades no longer get through.")
                .font(.system(size: 12.5, weight: .regular, design: .rounded))
                .foregroundStyle(Color.white.opacity(0.36))
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(cardBackground)
    }

    // MARK: - Notifications

    private var notificationsRow: some View {
        HStack(alignment: .center, spacing: 12) {
            VStack(alignment: .leading, spacing: 6) {
                Text("VPN status alerts")
                    .font(.system(size: 16, weight: .semibold, design: .rounded))
                    .foregroundStyle(.white)
                Text("Show system notifications when the VPN connects, reconnects, disconnects, or changes on schedule.")
                    .font(.system(size: 13, weight: .regular, design: .rounded))
                    .foregroundStyle(Color.white.opacity(0.48))
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 12)
            Toggle("", isOn: $settings.notificationsEnabled)
                .labelsHidden()
                .tint(accent)
                .onChange(of: settings.notificationsEnabled) { _, enabled in
                    if enabled {
                        VPNNotificationManager.requestAuthorization()
                    }
                    Task { await schedule.refresh() }
                }
        }
        .padding(16)
        .background(cardBackground)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("VPN status alerts")
        .accessibilityValue(settings.notificationsEnabled ? "On" : "Off")
    }

    private var cardBackground: some View {
        RoundedRectangle(cornerRadius: 16, style: .continuous)
            .fill(surfaceElevated)
            .overlay(
                RoundedRectangle(cornerRadius: 16, style: .continuous)
                    .stroke(Color.white.opacity(0.06), lineWidth: 1)
            )
    }

    private var isTunnelActive: Bool {
        switch vpn.status {
        case .connected, .connecting, .reasserting, .disconnecting:
            return true
        default:
            return false
        }
    }
}

#Preview {
    SettingsView()
}
