import Testing
@testable import Persona

/// A Mac registers with the hub as a desktop, never as the phone: the iPhone keeps the phone_* tools and its wake
/// pushes (docs/apple/mac.md「Mac is a desktop, not a phone」).
struct MacDesktopRegistrationTests {
    @Test func theMacRegistersAsADesktop() {
        let m = AppModel.registration(deviceName: "MacBook", tools: [["name": "phone_device_info"]], simulator: false)
        #expect(m["type"] as? String == "register_desktop")
        #expect(m["device_name"] as? String == "MacBook")
        #expect(m["tools"] == nil)
    }

    /// Its push id goes along: while this Mac is connected the hub leaves the agents' alerts to it (MacAttentionWatcher)
    /// instead of pushing them to it as well.
    @Test func itSaysWhichPushDeviceItIs() {
        let m = AppModel.registration(deviceName: "MacBook", tools: [], simulator: false, deviceID: "D1")
        #expect(m["device_id"] as? String == "D1")
    }
}
