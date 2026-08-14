//
//  JVPNTests.swift
//  JVPNTests
//

import Testing
@testable import JVPN

struct JVPNTests {

    @Test func udpOverTCPTransportIdentifier() {
        #expect(JVPNConnectionMode.standard.tunnelTransport == "auto")
        #expect(JVPNConnectionMode.udpOverTCP.tunnelTransport == "uot")
        #expect(JVPNConnectionMode.udpOverTCP.rawValue == "udpOverTCP")
        #expect(JVPNConnectionMode(rawValue: "udpOverTCP") == .udpOverTCP)
        #expect(JVPNConnectionMode(rawValue: "unknown") == nil)
    }

    @Test func experimentalModeLabels() {
        #expect(JVPNConnectionMode.udpOverTCP.title.contains("UDP-over-TCP"))
        #expect(JVPNConnectionMode.standard.title == "Standard")
        #expect(JVPNServiceConfig.uotPath == "/dns-query")
    }
}
