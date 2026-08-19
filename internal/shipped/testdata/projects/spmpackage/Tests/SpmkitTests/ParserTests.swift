import Testing

@testable import Spmkit

@Test("a malformed line yields nil")
func malformedLineYieldsNil() {
    #expect(Parser.fields(of: "no-tabs-here") == nil)
}
