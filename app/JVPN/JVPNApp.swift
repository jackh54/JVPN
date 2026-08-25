//
//  JVPNApp.swift
//  JVPN
//
//  Created by Jack Harris on 4/20/26.
//

import SwiftUI

#if os(macOS)
import AppKit

/// Prefer ordering existing windows on dock reopen so AppKit does not always go through
/// `_doOpenUntitled` → `showInitialWindows` (macOS 26 crash site with SwiftUI `.task`).
final class JVPNAppDelegate: NSObject, NSApplicationDelegate {
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if !flag {
            for window in sender.windows where window.canBecomeMain {
                window.makeKeyAndOrderFront(nil)
            }
        }
        return true
    }
}
#endif

@main
struct JVPNApp: App {
#if os(macOS)
    @NSApplicationDelegateAdaptor(JVPNAppDelegate.self) private var appDelegate
#endif

    var body: some Scene {
        WindowGroup {
            ContentView()
        }
#if os(macOS)
        .defaultSize(width: 420, height: 720)
#endif
    }
}
