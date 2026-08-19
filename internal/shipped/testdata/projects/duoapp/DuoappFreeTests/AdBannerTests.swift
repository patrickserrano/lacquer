import Testing

@testable import Duoapp

@Test("the free build compiles its ad surface")
func freeCompilesItsAdSurface() {
    #expect(Entitlement.free == Entitlement.free)
}
