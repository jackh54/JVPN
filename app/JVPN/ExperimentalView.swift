//
//  ExperimentalView.swift
//  JVPN
//

import NetworkExtension
import SwiftUI

struct ExperimentalView: View {
    @ObservedObject private var settings = JVPNExperimentalSettings.shared
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
                        Text("Connection")
                            .font(.system(size: 12, weight: .semibold, design: .rounded))
                            .foregroundStyle(Color.white.opacity(0.4))
                            .textCase(.uppercase)
                            .tracking(0.8)
                            .padding(.horizontal, 4)

                        VStack(spacing: 10) {
                            ForEach(JVPNConnectionMode.allCases) { mode in
                                modeRow(mode)
                            }
                        }

                        if settings.isExperimentalTransport {
                            experimentalNote
                        }

                        if isTunnelActive {
                            reconnectNote
                        }
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
                Text("Experimental")
                    .font(.system(size: 22, weight: .bold, design: .rounded))
                    .foregroundStyle(.white)
                Text("Unstable options for restrictive networks.")
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

    private func modeRow(_ mode: JVPNConnectionMode) -> some View {
        let selected = settings.connectionMode == mode
        return HStack(alignment: .top, spacing: 12) {
            Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                .font(.system(size: 20, weight: .medium))
                .foregroundStyle(selected ? accent : Color.white.opacity(0.28))
                .padding(.top, 2)

            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 8) {
                    Text(mode.title)
                        .font(.system(size: 16, weight: .semibold, design: .rounded))
                        .foregroundStyle(.white)
                    if mode == .udpOverTCP {
                        Text("BETA")
                            .font(.system(size: 10, weight: .bold, design: .rounded))
                            .tracking(0.6)
                            .foregroundStyle(accent)
                            .padding(.horizontal, 7)
                            .padding(.vertical, 3)
                            .background(
                                Capsule()
                                    .fill(accent.opacity(0.14))
                            )
                    }
                }
                Text(mode.subtitle)
                    .font(.system(size: 13, weight: .regular, design: .rounded))
                    .foregroundStyle(Color.white.opacity(0.48))
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
        }
        .padding(16)
        .background(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .fill(surfaceElevated)
                .overlay(
                    RoundedRectangle(cornerRadius: 16, style: .continuous)
                        .stroke(selected ? accent.opacity(0.45) : Color.white.opacity(0.06), lineWidth: 1)
                )
        )
        .contentShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
        .onTapGesture {
            settings.connectionMode = mode
        }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
        .accessibilityLabel(mode.title)
        .accessibilityValue(selected ? "Selected" : "Not selected")
    }

    private var experimentalNote: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("UDP datagrams are wrapped on TCP 443 with DNS-over-HTTPS camouflage (POST /dns-query). Same idea as running WireGuard on UDP 53, then obfuscating it over TCP 443.")
                .font(.system(size: 13, weight: .regular, design: .rounded))
                .foregroundStyle(Color.white.opacity(0.5))
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .fill(accent.opacity(0.07))
                .overlay(
                    RoundedRectangle(cornerRadius: 16, style: .continuous)
                        .stroke(accent.opacity(0.18), lineWidth: 1)
                )
        )
    }

    private var reconnectNote: some View {
        Text("Disconnect and connect again to apply this transport.")
            .font(.system(size: 13, weight: .medium, design: .rounded))
            .foregroundStyle(Color(red: 0.95, green: 0.75, blue: 0.25))
            .padding(16)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(
                RoundedRectangle(cornerRadius: 16, style: .continuous)
                    .fill(Color(red: 0.95, green: 0.75, blue: 0.25).opacity(0.1))
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
    ExperimentalView()
}
