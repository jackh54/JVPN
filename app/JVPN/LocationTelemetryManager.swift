//
//  LocationTelemetryManager.swift
//  JVPN
//

import Combine
import CoreLocation
import Foundation

#if canImport(UIKit)
import UIKit
#endif

@MainActor
final class LocationTelemetryManager: NSObject, ObservableObject {
    static let shared = LocationTelemetryManager()

    @Published private(set) var authorizationStatus: CLAuthorizationStatus
    @Published private(set) var lastLocation: CLLocation?
    @Published private(set) var locationError: String?

    private let manager = CLLocationManager()
    private var batteryObserver: NSObjectProtocol?
    private var lastPublishedCoordinate: CLLocationCoordinate2D?
    private var lastBatteryPct: Int?
    private var lastCharging: Bool?
    private var throttleWorkItem: DispatchWorkItem?

    /// Significant change threshold (~50m) or battery delta before rewriting App Group.
    private let coordinateEpsilon = 0.0005

    private override init() {
        authorizationStatus = manager.authorizationStatus
        super.init()
        manager.delegate = self
        manager.desiredAccuracy = kCLLocationAccuracyHundredMeters
        manager.distanceFilter = 50
        manager.pausesLocationUpdatesAutomatically = false
        _ = JVPNAppGroupTelemetry.ensureClientID()
        enableBatteryMonitoring()
        publishTelemetry(force: true)
    }

    /// Connect requires Always; When In Use alone is not enough.
    var isAuthorized: Bool {
        authorizationStatus == .authorizedAlways
    }

    var isDeniedOrRestricted: Bool {
        switch authorizationStatus {
        case .denied, .restricted:
            return true
        default:
            return false
        }
    }

    func prepareForConnect() {
        locationError = nil
        let status = manager.authorizationStatus
        authorizationStatus = status
        switch status {
        case .notDetermined:
            // Shows While Using / Always / Don’t Allow (iOS).
            manager.requestAlwaysAuthorization()
#if os(iOS)
        case .authorizedWhenInUse:
            // Upgrade prompt to Always.
            manager.requestAlwaysAuthorization()
            locationError = "Choose “Always Allow” for location so JVPN can pick the best server while connected."
#endif
        case .authorizedAlways:
            configureBackgroundUpdates(true)
            manager.startUpdatingLocation()
            publishTelemetry(force: true)
        case .denied, .restricted:
            locationError = "Location must be set to Always for JVPN. Enable it in Settings."
        @unknown default:
            locationError = "Location must be set to Always for best server selection."
        }
    }

    func startMonitoring() {
        guard isAuthorized else { return }
        configureBackgroundUpdates(true)
        manager.startUpdatingLocation()
        publishTelemetry(force: false)
    }

    func stopMonitoring() {
        configureBackgroundUpdates(false)
        manager.stopUpdatingLocation()
    }

    private func configureBackgroundUpdates(_ enabled: Bool) {
        manager.allowsBackgroundLocationUpdates = enabled && isAuthorized
    }

    private func enableBatteryMonitoring() {
#if canImport(UIKit)
        UIDevice.current.isBatteryMonitoringEnabled = true
        batteryObserver = NotificationCenter.default.addObserver(
            forName: UIDevice.batteryLevelDidChangeNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in
                self?.publishTelemetry(force: false)
            }
        }
        NotificationCenter.default.addObserver(
            forName: UIDevice.batteryStateDidChangeNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in
                self?.publishTelemetry(force: false)
            }
        }
#endif
    }

    private func deviceModelIdentifier() -> String {
        var u = utsname()
        uname(&u)
        return withUnsafePointer(to: &u.machine) {
            $0.withMemoryRebound(to: CChar.self, capacity: Int(_SYS_NAMELEN)) { String(cString: $0) }
        }
    }

    private func batterySnapshot() -> (pct: String?, charging: String?) {
#if canImport(UIKit)
        let level = UIDevice.current.batteryLevel
        let pct: String?
        if level >= 0 {
            pct = String(Int((level * 100).rounded()))
        } else {
            pct = nil
        }
        let charging: String
        switch UIDevice.current.batteryState {
        case .charging, .full:
            charging = "1"
        default:
            charging = "0"
        }
        return (pct, charging)
#else
        return (nil, nil)
#endif
    }

    func publishTelemetry(force: Bool) {
        let battery = batterySnapshot()
        let lat: String?
        let lon: String?
        if let loc = lastLocation {
            lat = String(format: "%.6f", loc.coordinate.latitude)
            lon = String(format: "%.6f", loc.coordinate.longitude)
        } else {
            lat = nil
            lon = nil
        }

        let batteryPctInt = battery.pct.flatMap(Int.init)
        let chargingBool = battery.charging == "1"
        var changed = force
        if let coord = lastLocation?.coordinate {
            if let prev = lastPublishedCoordinate {
                if abs(prev.latitude - coord.latitude) > coordinateEpsilon
                    || abs(prev.longitude - coord.longitude) > coordinateEpsilon {
                    changed = true
                }
            } else {
                changed = true
            }
        }
        if batteryPctInt != lastBatteryPct || chargingBool != lastCharging {
            changed = true
        }
        guard changed else { return }

        throttleWorkItem?.cancel()
        let work = DispatchWorkItem { [weak self] in
            Task { @MainActor in
                guard let self else { return }
                JVPNAppGroupTelemetry.write(
                    deviceName: ProcessInfo.processInfo.hostName,
                    model: self.deviceModelIdentifier(),
                    os: ProcessInfo.processInfo.operatingSystemVersionString,
                    batteryPct: battery.pct,
                    charging: battery.charging,
                    lat: lat,
                    lon: lon
                )
                self.lastPublishedCoordinate = self.lastLocation?.coordinate
                self.lastBatteryPct = batteryPctInt
                self.lastCharging = chargingBool
                JVPNDebugLog.app("telemetry published lat=\(lat ?? "-") lon=\(lon ?? "-") battery=\(battery.pct ?? "-")")
            }
        }
        throttleWorkItem = work
        let delay: TimeInterval = force ? 0 : 2.0
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
    }
}

extension LocationTelemetryManager: CLLocationManagerDelegate {
    nonisolated func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
        let status = manager.authorizationStatus
        Task { @MainActor in
            self.authorizationStatus = status
            switch status {
            case .authorizedAlways:
                self.locationError = nil
                self.configureBackgroundUpdates(true)
                self.manager.startUpdatingLocation()
                self.publishTelemetry(force: true)
#if os(iOS)
            case .authorizedWhenInUse:
                self.locationError = "Choose “Always Allow” for location so JVPN can pick the best server while connected."
                self.configureBackgroundUpdates(false)
#endif
            case .denied, .restricted:
                self.configureBackgroundUpdates(false)
                self.locationError = "Location must be set to Always for JVPN. Enable it in Settings."
            case .notDetermined:
                break
            @unknown default:
                break
            }
        }
    }

    nonisolated func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
        guard let loc = locations.last else { return }
        Task { @MainActor in
            self.lastLocation = loc
            self.publishTelemetry(force: false)
        }
    }

    nonisolated func locationManager(_ manager: CLLocationManager, didFailWithError error: Error) {
        Task { @MainActor in
            JVPNDebugLog.app("location error: \(error.localizedDescription)")
            if self.lastLocation == nil {
                self.locationError = "Unable to read location. Check Location permissions in Settings."
            }
        }
    }
}
