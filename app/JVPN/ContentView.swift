//
//  ContentView.swift
//  JVPN
//

import CoreLocation
import NetworkExtension
import SwiftUI

struct ContentView: View {
    @StateObject private var vpn = VPNManager.shared
    @StateObject private var location = LocationTelemetryManager.shared
    @State private var message: String = ""
    @State private var isWorking = false

    var body: some View {
        ZStack {
            backgroundGradient
                .ignoresSafeArea()

            VStack(spacing: 0) {
                Spacer(minLength: 24)

                brandBlock
                    .padding(.bottom, 28)

                Text(statusLabel(vpn.status))
                    .font(.system(size: 22, weight: .semibold, design: .rounded))
                    .foregroundStyle(statusColor)
                    .padding(.bottom, 8)

                Text(subtitleText)
                    .font(.system(size: 14, weight: .regular, design: .rounded))
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 36)
                    .frame(minHeight: 40)

                Spacer()

                connectButton
                    .padding(.bottom, 56)
            }
            .frame(maxWidth: 420)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .task {
            await vpn.load()
            location.prepareForConnect()
            if location.isAuthorized {
                location.startMonitoring()
            }
        }
    }

    private var brandBlock: some View {
        VStack(spacing: 14) {
            Image("BrandMark")
                .resizable()
                .scaledToFit()
                .frame(width: 88, height: 88)
                .clipShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
                .shadow(color: .black.opacity(0.18), radius: 16, y: 8)

            Text("JVPN")
                .font(.system(size: 40, weight: .bold, design: .rounded))
                .foregroundStyle(.primary)
                .tracking(1.2)
        }
    }

    private var connectButton: some View {
        Button {
            Task { await toggleVPN() }
        } label: {
            ZStack {
                Circle()
                    .fill(buttonFill)
                    .frame(width: 168, height: 168)
                    .shadow(color: buttonFill.opacity(0.45), radius: 22, y: 10)

                if isWorking || vpn.status == .connecting || vpn.status == .disconnecting {
                    ProgressView()
                        .controlSize(.large)
                        .tint(.white)
                } else {
                    VStack(spacing: 6) {
                        Image(systemName: isProtected ? "lock.fill" : "power")
                            .font(.system(size: 36, weight: .semibold))
                        Text(isProtected ? "Disconnect" : "Connect")
                            .font(.system(size: 16, weight: .semibold, design: .rounded))
                    }
                    .foregroundStyle(.white)
                }
            }
        }
        .buttonStyle(.plain)
        .disabled(isWorking || vpn.status == .connecting || vpn.status == .disconnecting)
        .accessibilityLabel(isProtected ? "Disconnect" : "Connect")
    }

    private var backgroundGradient: some View {
        LinearGradient(
            colors: [
                Color(red: 0.93, green: 0.95, blue: 0.97),
                Color(red: 0.86, green: 0.90, blue: 0.94),
                Color(red: 0.78, green: 0.85, blue: 0.90),
            ],
            startPoint: .topLeading,
            endPoint: .bottomTrailing
        )
    }

    private var isProtected: Bool {
        switch vpn.status {
        case .connected, .reasserting:
            return true
        default:
            return false
        }
    }

    private var buttonFill: Color {
        isProtected
            ? Color(red: 0.78, green: 0.22, blue: 0.24)
            : Color(red: 0.12, green: 0.55, blue: 0.42)
    }

    private var statusColor: Color {
        switch vpn.status {
        case .connected:
            return Color(red: 0.10, green: 0.48, blue: 0.36)
        case .connecting, .reasserting:
            return Color(red: 0.45, green: 0.40, blue: 0.15)
        default:
            return .secondary
        }
    }

    private var subtitleText: String {
        if JVPNServiceConfig.isPlaceholderConfiguration {
            return "Configuration error."
        }
        if let err = location.locationError, !location.isAuthorized {
            return err
        }
        if !message.isEmpty {
            return message
        }
        if let err = vpn.lastError {
            return err
        }
        switch vpn.status {
        case .connected:
            return "Your connection is encrypted."
        case .connecting:
            return "Establishing secure tunnel…"
        case .reasserting:
            return "Restoring your tunnel…"
        default:
            return "Tap Connect to protect your traffic."
        }
    }

    private func toggleVPN() async {
        message = ""
        switch vpn.status {
        case .connected, .reasserting:
            JVPNDebugLog.app("toggleVPN disconnect (status=\(String(describing: vpn.status)))")
            vpn.disconnect()
        default:
            location.prepareForConnect()
            guard location.isAuthorized else {
                if location.authorizationStatus == .notDetermined {
                    message = "Allow Location — choose Always — for best server selection."
                } else if location.authorizationStatus == .authorizedWhenInUse {
                    message = "Open Settings and set Location to Always for JVPN."
                } else {
                    message = location.locationError
                        ?? "Location must be Always for best server selection. Enable it in Settings."
                }
                return
            }
            if location.lastLocation == nil {
                location.startMonitoring()
                // Brief wait for a first fix when already authorized.
                try? await Task.sleep(nanoseconds: 800_000_000)
                if location.lastLocation == nil {
                    message = "Waiting for a GPS fix. Move near a window and try again."
                    return
                }
            }
            location.publishTelemetry(force: true)
            isWorking = true
            defer { isWorking = false }
            JVPNDebugLog.app("toggleVPN connect begin")
            do {
                try await vpn.connect()
                location.startMonitoring()
                JVPNDebugLog.app("toggleVPN connect finished without throw")
            } catch {
                JVPNDebugLog.app("toggleVPN connect error: \(error.localizedDescription)")
                message = error.localizedDescription
            }
        }
    }

    private func statusLabel(_ s: NEVPNStatus) -> String {
        switch s {
        case .invalid, .disconnected:
            return "Not Protected"
        case .connecting:
            return "Connecting"
        case .connected:
            return "Protected"
        case .reasserting:
            return "Reconnecting"
        case .disconnecting:
            return "Not Protected"
        @unknown default:
            return "Not Protected"
        }
    }
}

#Preview {
    ContentView()
}
