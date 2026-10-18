#!/usr/bin/env osascript -l JavaScript

// osascript calls run() itself, as the script's entry point; nothing here does.
// The web profile's biome.json lints this file wherever a web component sits at
// the repository root beside an iOS one, so it is kept clean under that config.
// biome-ignore lint/correctness/noUnusedVariables: osascript's entry point
function run() {
  const xcode = Application('Xcode')
  if (!xcode.running()) {
    return 'Xcode not running'
  }

  const systemEvents = Application('System Events')
  const xcodeProcess = systemEvents.processes.byName('Xcode')

  let approvedCount = 0

  try {
    for (const window of xcodeProcess.windows()) {
      for (const text of window.staticTexts()) {
        const val = text.value()
        // This is a consent dialog — never blanket-approve. Only click Allow
        // when the dialog names the expected requesting app (Claude).
        if (val?.includes('to access Xcode?') && /Claude/i.test(val)) {
          const allowButton = window.buttons.whose({ name: 'Allow' })[0]
          if (allowButton?.exists()) {
            allowButton.click()
            approvedCount++
            break
          }
        }
      }
    }
  } catch (e) {
    return `Error: ${e.message}`
  }

  return approvedCount > 0
    ? `Approved ${approvedCount} MCP connection(s)`
    : 'No pending MCP dialogs'
}
