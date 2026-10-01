import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Herdrmon bar widget: a themed Poké Ball glyph (Nerd Font md-pokeball).
// Click launches `herdrmon` floating via omarchy-launch-or-focus-tui. If the
// binary or the icon font isn't installed yet, click instead runs
// omarchy/install.sh (a sibling of this file) in a terminal.
BarWidget {
  id: root
  moduleName: "jonasbove.herdrmon"

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  // This plugin's own directory, so install.sh is found wherever `omarchy
  // plugin add` or a manual clone put it — never a hard-coded path.
  readonly property string pluginDir: Qt.resolvedUrl(".").toString().replace(/^file:\/\//, "")
  readonly property string installScript: root.pluginDir + "install.sh"

  property bool checked: false
  property bool installed: false

  function recheck() { if (!checkProc.running) checkProc.running = true }
  Component.onCompleted: root.recheck()

  // Re-check occasionally so the icon notices a setup that just finished in
  // another window, without polling aggressively.
  Timer { interval: 20000; running: true; repeat: true; onTriggered: root.recheck() }

  Process {
    id: checkProc
    command: ["sh", "-c", "command -v herdrmon >/dev/null 2>&1 && fc-list | grep -qi 'Herdrmon Icons' && echo yes || echo no"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        root.checked = true
        root.installed = String(text).trim() === "yes"
      }
    }
  }

  function activate() {
    if (root.installed) {
      Quickshell.execDetached(["omarchy-launch-or-focus-tui", "herdrmon"])
    } else {
      Quickshell.execDetached(["omarchy-launch-tui", "--app-id=herdrmon-setup", root.installScript])
      recheckSoon.restart()
    }
  }
  // Give the installer a moment to appear before the next poll notices it.
  Timer { id: recheckSoon; interval: 4000; onTriggered: root.recheck() }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "󰐝" // nf-md-pokeball
    tooltipText: !root.checked ? "Herdrmon" : (root.installed ? "Herdrmon" : "Herdrmon — click to set up")
    onPressed: function(b) {
      if (b === Qt.RightButton) root.recheck()
      else root.activate()
    }
  }
}
