//
//  JVPNTests.swift
//  JVPNTests
//

import Foundation
import Testing
@testable import JVPN

struct JVPNTests {

    @Test func transportIsUDPOverTCPOnly() {
        #expect(JVPNServiceConfig.transport == "uot")
        #expect(JVPNServiceConfig.uotPath == "/dns-query")
    }

    @Test func alwaysOnPolicyConnectsAfterOnTimeAndNeverDisconnects() throws {
        var policy = JVPNSchedulePolicy.fallback
        policy.autoConnect = true
        policy.autoDisconnect = false
        policy.onTime = "07:30"

        let before = try date(policy, year: 2026, month: 9, day: 22, hour: 6, minute: 0)
        let after = try date(policy, year: 2026, month: 9, day: 22, hour: 9, minute: 0)
        let evening = try date(policy, year: 2026, month: 9, day: 22, hour: 23, minute: 0)

        #expect(policy.intent(at: before) == JVPNScheduleIntent.none)
        #expect(policy.intent(at: after) == .connect)
        // Without auto-disconnect nothing ever asks for a teardown.
        #expect(policy.intent(at: evening) == .connect)
    }

    @Test func scheduledOffWindowAsksForDisconnect() throws {
        var policy = JVPNSchedulePolicy.fallback
        policy.autoConnect = true
        policy.autoDisconnect = true
        policy.onTime = "07:30"
        policy.offTime = "15:00"

        #expect(try policy.intent(at: date(policy, year: 2026, month: 9, day: 22, hour: 7, minute: 0)) == .disconnect)
        #expect(try policy.intent(at: date(policy, year: 2026, month: 9, day: 22, hour: 7, minute: 30)) == .connect)
        #expect(try policy.intent(at: date(policy, year: 2026, month: 9, day: 22, hour: 14, minute: 59)) == .connect)
        #expect(try policy.intent(at: date(policy, year: 2026, month: 9, day: 22, hour: 15, minute: 0)) == .disconnect)
        #expect(try policy.intent(at: date(policy, year: 2026, month: 9, day: 22, hour: 20, minute: 0)) == .disconnect)
    }

    @Test func overnightWindowSpansMidnight() throws {
        var policy = JVPNSchedulePolicy.fallback
        policy.autoConnect = true
        policy.autoDisconnect = true
        policy.onTime = "22:00"
        policy.offTime = "06:00"

        #expect(try policy.intent(at: date(policy, year: 2026, month: 9, day: 22, hour: 23, minute: 0)) == .connect)
        #expect(try policy.intent(at: date(policy, year: 2026, month: 9, day: 22, hour: 2, minute: 0)) == .connect)
        #expect(try policy.intent(at: date(policy, year: 2026, month: 9, day: 22, hour: 12, minute: 0)) == .disconnect)
    }

    @Test func inactiveDaysAreSkipped() throws {
        var policy = JVPNSchedulePolicy.fallback
        policy.autoConnect = true
        policy.days = [1, 2, 3, 4, 5] // weekdays

        // 2026-09-19 is a Saturday.
        let saturday = try date(policy, year: 2026, month: 9, day: 19, hour: 9, minute: 0)
        #expect(policy.intent(at: saturday) == JVPNScheduleIntent.none)

        let next = try #require(policy.nextOccurrence(of: policy.onTime, after: saturday))
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = policy.timeZone
        #expect(cal.component(.weekday, from: next) == 2) // Monday
        #expect(cal.component(.hour, from: next) == 7)
        #expect(cal.component(.minute, from: next) == 30)
    }

    @Test func decodesServerPolicyJSON() throws {
        let json = Data("""
        {"revision":4,"timezone":"America/Chicago","auto_connect":true,"on_time":"07:30",
         "auto_disconnect":true,"off_time":"15:00","days":[1,2,3,4,5],
         "notify_on":true,"notify_off":false,"updated_at":"2026-09-22T12:00:00Z"}
        """.utf8)
        let policy = try #require(JVPNAppGroupTelemetry.decodeSchedulePolicy(json))
        #expect(policy.revision == 4)
        #expect(policy.onTime == "07:30")
        #expect(policy.offTime == "15:00")
        #expect(policy.autoDisconnect)
        #expect(policy.notifyOn)
        #expect(!policy.notifyOff)
        #expect(policy.days == [1, 2, 3, 4, 5])
    }

    @Test func controlFrameCarriesPolicyBody() throws {
        var payload = Data([JVPNControlProtocol.magic, JVPNControlProtocol.typePolicy])
        payload.append(Data("{}".utf8))
        let message = try #require(JVPNControlProtocol.controlMessage(payload))
        #expect(message.type == JVPNControlProtocol.typePolicy)
        #expect(String(data: message.body, encoding: .utf8) == "{}")

        // An IPv4 packet must never be mistaken for a control frame.
        #expect(JVPNControlProtocol.controlMessage(Data([0x45, 0x00, 0x00, 0x28])) == nil)
    }

    private func date(
        _ policy: JVPNSchedulePolicy,
        year: Int, month: Int, day: Int, hour: Int, minute: Int
    ) throws -> Date {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = policy.timeZone
        var c = DateComponents()
        c.year = year; c.month = month; c.day = day; c.hour = hour; c.minute = minute
        return try #require(cal.date(from: c))
    }
}
