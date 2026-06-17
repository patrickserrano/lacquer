#!/usr/bin/env osascript -l JavaScript

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
        if (val && val.includes('to access Xcode?')) {
          const allowButton = window.buttons.whose({ name: 'Allow' })[0]
          if (allowButton && allowButton.exists()) {
            allowButton.click()
            approvedCount++
            break
          }
        }
      }
    }
  } catch (e) {
    return 'Error: ' + e.message
  }

  return approvedCount > 0
    ? `Approved ${approvedCount} MCP connection(s)`
    : 'No pending MCP dialogs'
}
