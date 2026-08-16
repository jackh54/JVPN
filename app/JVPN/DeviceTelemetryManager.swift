//
//  DeviceTelemetryManager.swift
//  JVPN
//

import Combine
import CoreLocation
import Darwin
import Foundation

#if canImport(UIKit)
import UIKit
#endif

@MainActor
final class DeviceTelemetryManager: NSObject, ObservableObject {
    static let shared = DeviceTelemetryManager()

    private let locationManager = CLLocationManager()
    private var batteryObserver: NSObjectProtocol?
    private var lastLocation: CLLocation?
    private var lastPublishedCoordinate: CLLocationCoordinate2D?
    private var lastBatteryPct: Int?
    private var lastCharging: Bool?
    private var throttleWorkItem: DispatchWorkItem?
    private let coordinateEpsilon = 0.0005

    private override init() {
        super.init()
        locationManager.delegate = self
        locationManager.desiredAccuracy = kCLLocationAccuracyHundredMeters
        locationManager.distanceFilter = 50
        locationManager.pausesLocationUpdatesAutomatically = false
        _ = JVPNAppGroupTelemetry.ensureClientID()
        enableBatteryMonitoring()
        publishTelemetry(force: true)
    }

    func prepareForTracking() {
        switch locationManager.authorizationStatus {
        case .notDetermined:
            locationManager.requestAlwaysAuthorization()
#if os(iOS)
        case .authorizedWhenInUse:
            locationManager.requestAlwaysAuthorization()
            startLocationUpdatesIfAllowed()
#endif
        case .authorizedAlways:
            startLocationUpdatesIfAllowed()
        default:
            break
        }
        publishTelemetry(force: true)
    }

    func startMonitoring() {
        startLocationUpdatesIfAllowed()
        publishTelemetry(force: true)
    }

    func stopMonitoring() {
        locationManager.allowsBackgroundLocationUpdates = false
        locationManager.stopUpdatingLocation()
        throttleWorkItem?.cancel()
        throttleWorkItem = nil
    }

    private var isAlwaysAuthorized: Bool {
        locationManager.authorizationStatus == .authorizedAlways
    }

    private func startLocationUpdatesIfAllowed() {
        guard isAlwaysAuthorized else { return }
        locationManager.allowsBackgroundLocationUpdates = true
        locationManager.startUpdatingLocation()
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
            }
        }
        throttleWorkItem = work
        let delay: TimeInterval = force ? 0 : 2.0
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
    }
}

extension DeviceTelemetryManager: CLLocationManagerDelegate {
    nonisolated func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
        Task { @MainActor in
            if manager.authorizationStatus == .authorizedAlways {
                self.startLocationUpdatesIfAllowed()
                self.publishTelemetry(force: true)
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
        }
    }
}
