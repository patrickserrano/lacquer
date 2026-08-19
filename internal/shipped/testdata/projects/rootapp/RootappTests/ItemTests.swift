import Testing

@testable import Rootapp

@Test("an item keeps the title it was given")
func itemKeepsItsTitle() {
    let item = Item(id: UUID(), title: "milk")
    #expect(item.title == "milk")
}
