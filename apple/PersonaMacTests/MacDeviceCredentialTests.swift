import Testing
@testable import Persona

/// The Mac's own credential is not a phone's: Settings › Devices shows it as a computer, and only the iPhone is the phone
/// (docs/apple/mac.md「Mac is a desktop, not a phone」). The hub keeps kinds other than phone / watch as "other", with full rights.
struct MacDeviceCredentialTests {
    @Test func theMacsCredentialIsNotAPhone() {
        #expect(AppModel.credentialKind == "desktop")
    }
}
