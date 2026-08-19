import Testing

@testable import Multistack

@Test("the app builds a scene")
func theAppBuildsAScene() {
    #expect(MultistackApp() != nil)
}
