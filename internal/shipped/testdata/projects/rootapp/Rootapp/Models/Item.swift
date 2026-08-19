import Foundation

/// One row in the list.
struct Item: Identifiable, Hashable {
    let id: UUID
    let title: String
}
