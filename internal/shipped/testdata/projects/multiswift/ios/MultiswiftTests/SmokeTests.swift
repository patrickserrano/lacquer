import Testing

@testable import Multiswift

@Test("the app builds a scene")
func theAppBuildsAScene() {
    #expect(MultiswiftApp() != nil)
}
