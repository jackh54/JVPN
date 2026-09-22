//
//  JVPNSchedulePolicy.swift
//  JVPNPacketTunnel
//
//  The VPN on/off schedule authored in the admin dashboard and pushed to the
//  tunnel as a control frame (0xC0 0x03 + JSON). Cached in the App Group so the
//  app can act on it while the tunnel is down.
//
//  Keep in sync with JVPN/JVPNSchedulePolicy.swift.
//

import Foundation

/// What the schedule wants the tunnel to be doing right now.
enum JVPNScheduleIntent: Equatable {
    /// Bring the tunnel up (and keep it up).
    case connect
    /// Tear the tunnel down — the off window is in effect.
    case disconnect
    /// The schedule has no opinion; leave the tunnel alone.
    case none
}

struct JVPNSchedulePolicy: Codable, Equatable {
    var revision: Int
    var timezone: String
    var autoConnect: Bool
    var onTime: String
    var autoDisconnect: Bool
    var offTime: String
    var days: [Int]?
    var notifyOn: Bool
    var notifyOff: Bool

    enum CodingKeys: String, CodingKey {
        case revision
        case timezone
        case autoConnect = "auto_connect"
        case onTime = "on_time"
        case autoDisconnect = "auto_disconnect"
        case offTime = "off_time"
        case days
        case notifyOn = "notify_on"
        case notifyOff = "notify_off"
    }

    /// Matches the server default: on at 07:30 America/Chicago, never auto-off.
    static let fallback = JVPNSchedulePolicy(
        revision: 0,
        timezone: "America/Chicago",
        autoConnect: true,
        onTime: "07:30",
        autoDisconnect: false,
        offTime: "15:00",
        days: nil,
        notifyOn: true,
        notifyOff: true
    )

    // MARK: - Calendar helpers

    var timeZone: TimeZone {
        TimeZone(identifier: timezone) ?? TimeZone(identifier: "America/Chicago") ?? .current
    }

    private var calendar: Calendar {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = timeZone
        return cal
    }

    /// Minutes past local midnight, or nil when the string is not "HH:MM".
    static func minutes(from hhmm: String) -> Int? {
        let parts = hhmm.split(separator: ":")
        guard parts.count == 2,
              let h = Int(parts[0]), (0...23).contains(h),
              let m = Int(parts[1]), (0...59).contains(m)
        else { return nil }
        return h * 60 + m
    }

    var onMinutes: Int? { Self.minutes(from: onTime) }
    var offMinutes: Int? { Self.minutes(from: offTime) }

    /// `weekday` is 0 = Sunday … 6 = Saturday. An empty day list means every day.
    func isActiveDay(_ weekday: Int) -> Bool {
        guard let days, !days.isEmpty else { return true }
        return days.contains(weekday)
    }

    private func weekdayIndex(for date: Date) -> Int {
        // Calendar weekday is 1 = Sunday; the policy uses 0 = Sunday.
        calendar.component(.weekday, from: date) - 1
    }

    private func minutesOfDay(for date: Date) -> Int {
        let c = calendar.dateComponents([.hour, .minute], from: date)
        return (c.hour ?? 0) * 60 + (c.minute ?? 0)
    }

    // MARK: - Intent

    /// Whether `now` falls inside the configured on-window. Only meaningful when
    /// `autoDisconnect` is set; without it the tunnel is simply always-on.
    func isInsideOnWindow(_ now: Date) -> Bool {
        guard let on = onMinutes, let off = offMinutes, on != off else {
            return isActiveDay(weekdayIndex(for: now))
        }
        let today = weekdayIndex(for: now)
        let nowMinutes = minutesOfDay(for: now)
        if on < off {
            return isActiveDay(today) && nowMinutes >= on && nowMinutes < off
        }
        // Overnight window, e.g. on 22:00 → off 06:00.
        if nowMinutes >= on {
            return isActiveDay(today)
        }
        if nowMinutes < off {
            return isActiveDay((today + 6) % 7)
        }
        return false
    }

    func intent(at now: Date = Date()) -> JVPNScheduleIntent {
        if autoDisconnect {
            if isInsideOnWindow(now) {
                return autoConnect ? .connect : .none
            }
            return .disconnect
        }
        // No auto-off configured: the tunnel is always-on from the on-time onward.
        guard autoConnect, let on = onMinutes else { return .none }
        if isActiveDay(weekdayIndex(for: now)), minutesOfDay(for: now) >= on {
            return .connect
        }
        return .none
    }

    // MARK: - Next transitions

    /// Next instant at which "HH:MM" occurs on an enabled weekday.
    func nextOccurrence(of hhmm: String, after now: Date = Date()) -> Date? {
        guard let total = Self.minutes(from: hhmm) else { return nil }
        let cal = calendar
        var components = DateComponents()
        components.hour = total / 60
        components.minute = total % 60
        components.second = 0
        for offset in 0...8 {
            guard let day = cal.date(byAdding: .day, value: offset, to: now) else { continue }
            var dayComponents = cal.dateComponents([.year, .month, .day], from: day)
            dayComponents.hour = components.hour
            dayComponents.minute = components.minute
            dayComponents.second = 0
            guard let candidate = cal.date(from: dayComponents), candidate > now else { continue }
            guard isActiveDay(weekdayIndex(for: candidate)) else { continue }
            return candidate
        }
        return nil
    }

    /// Most recent instant at which "HH:MM" occurred on an enabled weekday.
    func previousOccurrence(of hhmm: String, before now: Date = Date()) -> Date? {
        guard let total = Self.minutes(from: hhmm) else { return nil }
        let cal = calendar
        for offset in 0...8 {
            guard let day = cal.date(byAdding: .day, value: -offset, to: now) else { continue }
            var dayComponents = cal.dateComponents([.year, .month, .day], from: day)
            dayComponents.hour = total / 60
            dayComponents.minute = total % 60
            dayComponents.second = 0
            guard let candidate = cal.date(from: dayComponents), candidate <= now else { continue }
            guard isActiveDay(weekdayIndex(for: candidate)) else { continue }
            return candidate
        }
        return nil
    }

    var nextOnDate: Date? {
        autoConnect ? nextOccurrence(of: onTime) : nil
    }

    var nextOffDate: Date? {
        autoDisconnect ? nextOccurrence(of: offTime) : nil
    }

    /// Repeating-notification trigger components for a daily time, in the policy
    /// timezone. One entry per active weekday (a single entry when every day is on).
    func triggerComponents(for hhmm: String) -> [DateComponents] {
        guard let total = Self.minutes(from: hhmm) else { return [] }
        var base = DateComponents()
        base.hour = total / 60
        base.minute = total % 60
        base.timeZone = timeZone
        guard let days, !days.isEmpty, days.count < 7 else { return [base] }
        return days.sorted().map { day in
            var c = base
            c.weekday = day + 1 // DateComponents weekday is 1 = Sunday
            return c
        }
    }

    // MARK: - Display

    /// "7:30 AM" in the viewer's locale, from the policy's wall-clock time.
    func localizedTime(_ hhmm: String) -> String {
        guard let total = Self.minutes(from: hhmm) else { return hhmm }
        var c = DateComponents()
        c.hour = total / 60
        c.minute = total % 60
        guard let date = Calendar(identifier: .gregorian).date(from: c) else { return hhmm }
        let fmt = DateFormatter()
        fmt.timeStyle = .short
        fmt.dateStyle = .none
        return fmt.string(from: date)
    }

    var timeZoneAbbreviation: String {
        timeZone.abbreviation() ?? timezone
    }
}
