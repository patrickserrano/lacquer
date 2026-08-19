import RootappKit
import SwiftUI

/// The root view.
struct ContentView: View {
    @State private var items: [Item] = []

    var body: some View {
        List(items) { item in
            Text(item.title)
        }
    }
}
