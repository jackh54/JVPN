//
//  ContentView.swift
//  JVPN
//

import CoreLocation
import NetworkExtension
import SwiftUI

struct ContentView: View {
    // Shared singletons must not be owned by @StateObject (lifetime/identity glitches on macOS 26).
    @ObservedObject private var vpn = VPNManager.shared
    @ObservedObject private var location = LocationTelemetryManager.shared
    @State private var message: String = ""
    @State private var isWorking = false
    @State private var connectPressed = false
    @State private var pulseScale: CGFloat = 1.0
    @State private var ringRotation: Double = 0

    private let accent = Color(red: 0.24, green: 0.87, blue: 0.60)
    private let danger = Color(red: 0.95, green: 0.35, blue: 0.42)
    private let surfaceElevated = Color(red: 0.15, green: 0.17, blue: 0.21)

    var body: some View {
        ZStack {
            backgroundLayer
                .ignoresSafeArea()

            VStack(spacing: 0) {
                headerBar
                    .padding(.top, 12)
                    .padding(.horizontal, 24)

                Spacer(minLength: 20)

                statusHero
                    .padding(.horizontal, 24)

                Spacer()

                connectControl
                    .padding(.bottom, 48)

                footerHint
                    .padding(.horizontal, 32)
                    .padding(.bottom, 36)
            }
            .frame(maxWidth: 440)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .preferredColorScheme(.dark)
        .task {
            await vpn.load()
            location.prepareForConnect()
            if location.isAuthorized {
                location.startMonitoring()
            }
            updateProtectionAnimations(isProtected)
        }
        .onChange(of: isProtected) { _, protected in
            updateProtectionAnimations(protected)
        }
    }

    // MARK: - Background

    private var backgroundLayer: some View {
        ZStack {
            Color(red: 0.06, green: 0.07, blue: 0.09)

            RadialGradient(
                colors: [accent.opacity(isProtected ? 0.12 : 0.06), .clear],
                center: .top,
                startRadius: 20,
                endRadius: 420
            )

            RadialGradient(
                colors: [Color(red: 0.2, green: 0.35, blue: 0.7).opacity(0.08), .clear],
                center: .bottomTrailing,
                startRadius: 10,
                endRadius: 350
            )

            if isProtected {
                Circle()
                    .fill(accent.opacity(0.04))
                    .frame(width: 500, height: 500)
                    .blur(radius: 80)
                    .offset(y: 60)
                    .scaleEffect(pulseScale)
            }
        }
    }

    // MARK: - Header

    private var headerBar: some View {
        HStack(spacing: 12) {
            Image("BrandMark")
                .resizable()
                .scaledToFit()
                .frame(width: 36, height: 36)
                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))

            Text("JVPN")
                .font(.system(size: 20, weight: .bold, design: .rounded))
                .foregroundStyle(.white)

            Spacer()

            statusPill
        }
    }

    private var statusPill: some View {
        HStack(spacing: 7) {
            Circle()
                .fill(statusDotColor)
                .frame(width: 7, height: 7)
                .shadow(color: statusDotColor.opacity(0.6), radius: 4)

            Text(statusLabel(vpn.status))
                .font(.system(size: 12, weight: .semibold, design: .rounded))
                .foregroundStyle(statusDotColor)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
        .background(
            Capsule()
                .fill(statusDotColor.opacity(0.12))
                .overlay(Capsule().stroke(statusDotColor.opacity(0.25), lineWidth: 1))
        )
    }

    // MARK: - Status Hero

    private var statusHero: some View {
        VStack(spacing: 20) {
            ZStack {
                if isProtected {
                    Image(systemName: "shield.checkered")
                        .font(.system(size: 56, weight: .light))
                        .foregroundStyle(accent.opacity(0.9))
                        .symbolEffect(.pulse, options: .repeating)
                } else {
                    Image(systemName: "shield.slash")
                        .font(.system(size: 56, weight: .light))
                        .foregroundStyle(Color.white.opacity(0.25))
                }
            }
            .frame(height: 72)

            VStack(spacing: 8) {
                Text(heroTitle)
                    .font(.system(size: 28, weight: .bold, design: .rounded))
                    .foregroundStyle(.white)
                    .multilineTextAlignment(.center)

                Text(subtitleText)
                    .font(.system(size: 15, weight: .regular, design: .rounded))
                    .foregroundStyle(Color.white.opacity(0.5))
                    .multilineTextAlignment(.center)
                    .lineSpacing(3)
                    .frame(minHeight: 44)
            }

            if isProtected {
                connectionInfoCard
                    .transition(.opacity.combined(with: .move(edge: .bottom)))
            }
        }
        .animation(.spring(response: 0.45, dampingFraction: 0.82), value: isProtected)
    }

    private var heroTitle: String {
        switch vpn.status {
        case .connected:
            return "You're Protected"
        case .connecting:
            return "Connecting…"
        case .reasserting:
            return "Reconnecting…"
        case .disconnecting:
            return "Disconnecting…"
        default:
            return "Not Protected"
        }
    }

    private var connectionInfoCard: some View {
        HStack(spacing: 0) {
            infoCell(icon: "lock.fill", label: "Encrypted", value: "AES-256")
            divider
            infoCell(icon: "network", label: "Tunnel", value: "Active")
            divider
            infoCell(icon: "location.fill", label: "GPS", value: location.isAuthorized ? "On" : "Off")
        }
        .padding(.vertical, 14)
        .background(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .fill(surfaceElevated)
                .overlay(
                    RoundedRectangle(cornerRadius: 16, style: .continuous)
                        .stroke(Color.white.opacity(0.06), lineWidth: 1)
                )
        )
    }

    private func infoCell(icon: String, label: String, value: String) -> some View {
        VStack(spacing: 5) {
            Image(systemName: icon)
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(accent.opacity(0.8))
            Text(label)
                .font(.system(size: 10, weight: .medium, design: .rounded))
                .foregroundStyle(Color.white.opacity(0.4))
                .textCase(.uppercase)
                .tracking(0.5)
            Text(value)
                .font(.system(size: 13, weight: .semibold, design: .rounded))
                .foregroundStyle(.white.opacity(0.85))
        }
        .frame(maxWidth: .infinity)
    }

    private var divider: some View {
        Rectangle()
            .fill(Color.white.opacity(0.06))
            .frame(width: 1, height: 36)
    }

    // MARK: - Connect Control

    private var isConnectBusy: Bool {
        isWorking || vpn.status == .connecting || vpn.status == .disconnecting
    }

    /// Avoid SwiftUI `Button` / `_ButtonGesture` → `MainActor.assumeIsolated` on macOS 26,
    /// which can SIGSEGV at a near-null address while dispatching the click.
    private var connectControl: some View {
        ZStack {
            if isProtected {
                Circle()
                    .stroke(accent.opacity(0.2), lineWidth: 2)
                    .frame(width: 200, height: 200)
                    .scaleEffect(pulseScale)

                Circle()
                    .stroke(
                        AngularGradient(
                            colors: [accent.opacity(0.5), accent.opacity(0.05), accent.opacity(0.5)],
                            center: .center
                        ),
                        lineWidth: 2
                    )
                    .frame(width: 188, height: 188)
                    .rotationEffect(.degrees(ringRotation))
            }

            Circle()
                .fill(
                    RadialGradient(
                        colors: [buttonFill.opacity(0.9), buttonFill],
                        center: .topLeading,
                        startRadius: 0,
                        endRadius: 100
                    )
                )
                .frame(width: 156, height: 156)
                .shadow(color: buttonFill.opacity(0.45), radius: 24, y: 8)
                .overlay(
                    Circle()
                        .stroke(Color.white.opacity(0.12), lineWidth: 1)
                )

            if isConnectBusy {
                ProgressView()
                    .controlSize(.large)
                    .tint(.white)
            } else {
                VStack(spacing: 8) {
                    Image(systemName: "power")
                        .font(.system(size: 32, weight: .medium))
                    Text(isProtected ? "Disconnect" : "Connect")
                        .font(.system(size: 14, weight: .semibold, design: .rounded))
                        .tracking(0.3)
                }
                .foregroundStyle(.white)
            }
        }
        .frame(width: 200, height: 200)
        .contentShape(Circle())
        .scaleEffect(connectPressed ? 0.94 : 1.0)
        .animation(.spring(response: 0.25, dampingFraction: 0.7), value: connectPressed)
        .opacity(isConnectBusy ? 0.7 : 1.0)
        .allowsHitTesting(!isConnectBusy)
        .gesture(
            DragGesture(minimumDistance: 0)
                .onChanged { _ in
                    if !connectPressed { connectPressed = true }
                }
                .onEnded { _ in
                    connectPressed = false
                    guard !isConnectBusy else { return }
                    Task { @MainActor in
                        await toggleVPN()
                    }
                }
        )
        .accessibilityElement(children: .ignore)
        .accessibilityAddTraits(.isButton)
        .accessibilityLabel(isProtected ? "Disconnect" : "Connect")
        .accessibilityAction {
            guard !isConnectBusy else { return }
            Task { @MainActor in
                await toggleVPN()
            }
        }
    }

    private var footerHint: some View {
        Group {
            if !isProtected && message.isEmpty && vpn.lastError == nil && !JVPNServiceConfig.isPlaceholderConfiguration {
                Text("Your traffic is unencrypted until you connect.")
                    .font(.system(size: 12, weight: .regular, design: .rounded))
                    .foregroundStyle(Color.white.opacity(0.3))
            }
        }
    }

    // MARK: - Computed Properties

    private var isProtected: Bool {
        switch vpn.status {
        case .connected, .reasserting:
            return true
        default:
            return false
        }
    }

    private var buttonFill: Color {
        isProtected ? danger : accent
    }

    private var statusDotColor: Color {
        switch vpn.status {
        case .connected:
            return accent
        case .connecting, .reasserting:
            return Color(red: 0.95, green: 0.75, blue: 0.25)
        case .disconnecting:
            return Color.white.opacity(0.4)
        default:
            return Color.white.opacity(0.35)
        }
    }

    private var subtitleText: String {
        if JVPNServiceConfig.isPlaceholderConfiguration {
            return "Configuration error — check your server token."
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
            return "All network traffic is routed through the secure tunnel."
        case .connecting:
            return "Establishing encrypted tunnel to server…"
        case .reasserting:
            return "Restoring your secure connection…"
        default:
            return "Tap the button below to encrypt your connection."
        }
    }

    // MARK: - Actions

    private func toggleVPN() async {
        message = ""
        switch vpn.status {
        case .connected, .reasserting:
            JVPNDebugLog.app("toggleVPN disconnect (status=\(String(describing: vpn.status)))")
            vpn.disconnect()
        default:
            location.prepareForConnect()
            guard location.isAuthorized else {
                switch location.authorizationStatus {
                case .notDetermined:
                    message = "Allow Location — choose Always — for best server selection."
#if os(iOS)
                case .authorizedWhenInUse:
                    message = "Open Settings and set Location to Always for JVPN."
#endif
                default:
                    message = location.locationError
                        ?? "Location must be Always for best server selection. Enable it in Settings."
                }
                return
            }
            if location.lastLocation == nil {
                location.startMonitoring()
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

    private func updateProtectionAnimations(_ protected: Bool) {
        if protected {
            withAnimation(.easeInOut(duration: 1.6).repeatForever(autoreverses: true)) {
                pulseScale = 1.08
            }
            withAnimation(.linear(duration: 8).repeatForever(autoreverses: false)) {
                ringRotation = 360
            }
        } else {
            pulseScale = 1.0
            ringRotation = 0
        }
    }

    private func statusLabel(_ s: NEVPNStatus) -> String {
        switch s {
        case .invalid, .disconnected:
            return "Offline"
        case .connecting:
            return "Connecting"
        case .connected:
            return "Protected"
        case .reasserting:
            return "Reconnecting"
        case .disconnecting:
            return "Disconnecting"
        @unknown default:
            return "Offline"
        }
    }
}

#Preview {
    ContentView()
}
