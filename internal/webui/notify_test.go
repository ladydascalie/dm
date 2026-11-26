package webui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The notify half of the page, run in node against a browser stand-in. The page
// owns this rule, so the test drives the page's own functions rather than a
// copy: the source is cut out of app.js by name and assembled here.

// notifyHarness is the browser the page's functions see. Only the calls they
// make are here, plus the two module variables the page declares at top level.
// The device's subscription counts its own unsubscribe, and the subscribe POST
// rejects the way an aborted request or a dropped connection does.
const notifyHarness = `
const NOTIFY_KEY = "dm.notify";
let notifyOn = false;
let notifyBusy = false;

const button = {
  dataset: {},
  title: "",
  classList: { toggle() {} },
  setAttribute(name, value) { this[name] = value; },
};
function $(id) { return id === "notify-toggle" ? button : null; }
const banners = [];
function banner(msg) { banners.push(msg || ""); }

const memory = new Map();
const localStorage = {
  getItem(key) { return memory.has(key) ? memory.get(key) : null; },
  setItem(key, value) { memory.set(key, String(value)); },
};

const device = {
  endpoint: "https://push.example.com/device",
  dropped: 0,
  async unsubscribe() { this.dropped++; return true; },
  getKey() { return new Uint8Array([4, 1, 2, 3]).buffer; },
};
const registration = {
  pushManager: {
    async getSubscription() { return device; },
    async subscribe() { return device; },
  },
};
const navigator = {
  serviceWorker: {
    async register() { return registration; },
    async getRegistration() { return registration; },
  },
};
const Notification = {
  permission: "granted",
  async requestPermission() { return "granted"; },
};
const PushManager = function () {};
const window = { PushManager, Notification };

async function fetchAPI(path) {
  if (path === "/api/push/config") {
    return { ok: true, async json() { return { enabled: true, publicKey: "BAQE" }; } };
  }
  throw new TypeError("network error");
}
`

// notifyFunctions is every function the tap reaches, in the page's own source.
var notifyFunctions = []string{
	"notifySupported", "paintNotify", "storedNotify", "setStoredNotify",
	"swRegistration", "dropSubscription", "b64url", "fromB64url",
	"subscribePush", "unsubscribePush", "toggleNotify",
}

// notifyFunc returns one function's source with the async keyword the page
// declared for it. funcSource starts at "function ", which would drop the
// keyword and leave an await outside an async body.
func notifyFunc(t *testing.T, src, name string) string {
	t.Helper()
	body := funcSource(t, src, name)
	if strings.Contains(src, "async function "+name+"(") {
		return "async " + body
	}
	return body
}

// notifyCheck is one tap with the subscribe POST rejecting, then the three
// facts the finding named: the device dropped its subscription, the page does
// not believe it is subscribed, and the button reads off.
const notifyCheck = `
await toggleNotify();
const problems = [];
if (device.dropped !== 1) {
  problems.push("device subscriptions dropped = " + device.dropped + ", want 1: the page kept a subscription dm never stored");
}
if (notifyOn !== false) {
  problems.push("notifyOn = " + notifyOn + ", want false");
}
if (button.dataset.state !== "off") {
  problems.push("the button reads " + JSON.stringify(button.dataset.state) + ", want off");
}
if (!banners.some((line) => line.startsWith("notifications failed"))) {
  problems.push("banners = " + JSON.stringify(banners) + ", want the failed notice");
}
if (problems.length) {
  console.error(problems.join("\n"));
  process.exit(1);
}
console.log("ok");
`

// TestSubscribeFailureDropsTheDevice proves a subscribe POST that rejects, not
// only one that answers, is treated like a refusal: the device's subscription
// goes and the switch stays off. A phone whose POST never arrived must not read
// as subscribed at the next load, because dm has no row to deliver to and the
// switch reads the device.
func TestSubscribeFailureDropsTheDevice(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH: the page's behaviour needs a JavaScript runtime")
	}
	src := appScript(t)
	var page strings.Builder
	page.WriteString(notifyHarness)
	for _, name := range notifyFunctions {
		page.WriteString(notifyFunc(t, src, name))
		page.WriteString("\n")
	}
	page.WriteString(notifyCheck)

	path := filepath.Join(t.TempDir(), "notify.mjs")
	if err := os.WriteFile(path, []byte(page.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, path).CombinedOutput(); err != nil {
		t.Fatalf("the page's tap failed the rule: %v\n%s", err, out)
	}
}
