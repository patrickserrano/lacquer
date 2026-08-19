import Testing

@testable import Duoapp

@Test("the paid build defaults to the pro entitlement")
func paidDefaultsToPro() {
    #expect(Entitlement.pro != Entitlement.free)
}
