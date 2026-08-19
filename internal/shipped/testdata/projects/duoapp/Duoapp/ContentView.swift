import SwiftUI

/// The root view.
struct ContentView: View {
    var body: some View {
        VStack {
            Text("Duoapp")
            #if FREE
                AdBanner()
            #endif
        }
    }
}
