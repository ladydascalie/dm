package webui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The notify half of the page, run in node against a browser stand-in. The page
// owns these rules, so the tests drive the page's own functions rather than a
// copy: the source is cut out of app.js by name and assembled here. One
// scenario per run, named on the command line.

// notifyHarness is the browser the page's functions see. Only the calls they
// make are here, plus the two module variables the page declares at top level.
// The device's subscription counts its own drops and can refuse them, and the
// subscribe POST can reject the way an aborted request does.
const notifyHarness = `
const NOTIFY_KEY = "dm.notify";
let notifyOn = false;
let notifyBusy = false;

// world is what the scenario set up and what the page did to it.
const world = {
  refusesDrop: false,
  postFails: false,
  posts: [],
  banners: [],
};

const button = {
  dataset: {},
  title: "",
  classList: { toggle() {} },
  setAttribute(name, value) { this[name] = value; },
};
function $(id) { return id === "notify-toggle" ? button : null; }
function banner(msg) { world.banners.push(msg || ""); }

const memory = new Map();
const localStorage = {
  getItem(key) { return memory.has(key) ? memory.get(key) : null; },
  setItem(key, value) { memory.set(key, String(value)); },
};

const device = {
  endpoint: "https://push.example.com/device",
  drops: 0,
  async unsubscribe() {
    this.drops++;
    if (world.refusesDrop) throw new DOMException("the push service refused", "AbortError");
    return true;
  },
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

// fetchAPI answers what the scenario needs, and records every write the page
// sends to dm.
async function fetchAPI(path) {
  if (path === "/api/push/config") {
    return { ok: true, async json() { return { enabled: true, publicKey: "BAQE" }; } };
  }
  if (path === "/api/push/subscribe" && world.postFails) {
    throw new TypeError("network error");
  }
  world.posts.push(path);
  return { ok: true, status: 204, async text() { return ""; } };
}

const problems = [];
function want(condition, message) {
  if (!condition) problems.push(message);
}
`

// notifyFunctions is every function a tap reaches, in the page's own source.
var notifyFunctions = []string{
	"notifySupported", "paintNotify", "storedNotify", "setStoredNotify",
	"swRegistration", "dropSubscription", "b64url", "fromB64url",
	"subscribePush", "unsubscribePush", "toggleNotify",
}

// notifyChecks is one tap per scenario, and the facts that tap must leave.
const notifyChecks = `
const scenario = process.argv[2] || "";
if (scenario === "subscribe-rejected") {
  // Finding 1: the POST rejects, the device lets go, the switch stays off.
  world.postFails = true;
  await toggleNotify();
  want(device.drops === 1, "device drops = " + device.drops + ", want 1: the page kept a subscription dm never stored");
  want(notifyOn === false, "notifyOn = " + notifyOn + ", want false");
  want(button.dataset.state === "off", "the button reads " + button.dataset.state + ", want off");
  want(world.banners.some((line) => line.startsWith("notifications failed")), "banners = " + JSON.stringify(world.banners));
  want(!world.banners.some((line) => line.includes("kept the subscription")), "the banner names a kept subscription the device dropped");
} else if (scenario === "subscribe-rejected-kept") {
  // The POST rejects and the device refuses the drop: the page cannot repair
  // it, so it must say the subscription is still there.
  world.postFails = true;
  world.refusesDrop = true;
  await toggleNotify();
  want(device.drops === 1, "device drops = " + device.drops + ", want 1");
  want(notifyOn === false, "notifyOn = " + notifyOn + ", want false: dm has no row");
  want(world.banners.some((line) => line.includes("kept the subscription")), "banners = " + JSON.stringify(world.banners) + ", want the kept subscription named");
} else if (scenario === "tap-off-refused") {
  // The device refuses the drop, so dm must keep the row and the switch must
  // stay on: dm cannot reach a phone that still holds the subscription.
  notifyOn = true;
  memory.set(NOTIFY_KEY, "on");
  world.refusesDrop = true;
  await toggleNotify();
  want(world.posts.length === 0, "posts = " + JSON.stringify(world.posts) + ", want none: dm must not drop the row for a device that kept it");
  want(device.drops === 1, "device drops = " + device.drops + ", want 1");
  want(notifyOn === true, "notifyOn = " + notifyOn + ", want true");
  want(memory.get(NOTIFY_KEY) === "on", "stored = " + memory.get(NOTIFY_KEY) + ", want on");
  want(button.dataset.state === "on", "the button reads " + button.dataset.state + ", want on");
  want(world.banners.some((line) => line.includes("still on")), "banners = " + JSON.stringify(world.banners) + ", want the refusal named");
} else if (scenario === "tap-off-dropped") {
  // The device lets go, so dm is told and the switch records off.
  notifyOn = true;
  memory.set(NOTIFY_KEY, "on");
  await toggleNotify();
  want(device.drops === 1, "device drops = " + device.drops + ", want 1");
  want(world.posts.includes("/api/push/unsubscribe"), "posts = " + JSON.stringify(world.posts) + ", want the unsubscribe");
  want(notifyOn === false, "notifyOn = " + notifyOn + ", want false");
  want(memory.get(NOTIFY_KEY) === "off", "stored = " + memory.get(NOTIFY_KEY) + ", want off");
  want(button.dataset.state === "off", "the button reads " + button.dataset.state + ", want off");
  want(world.banners.some((line) => line.includes("notifications off")), "banners = " + JSON.stringify(world.banners));
} else {
  problems.push("unknown scenario " + JSON.stringify(scenario));
}

if (problems.length) {
  console.error(problems.join("\n"));
  process.exit(1);
}
console.log("ok");
`

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

// notifyTap runs one scenario of the page's notify code in node and fails the
// test with the page's own account of what it did wrong. A machine without node
// skips: the rule lives in the page, and nothing else can drive it.
func notifyTap(t *testing.T, scenario string) {
	t.Helper()
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
	page.WriteString(notifyChecks)

	path := filepath.Join(t.TempDir(), "notify.mjs")
	if err := os.WriteFile(path, []byte(page.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, path, scenario).CombinedOutput(); err != nil {
		t.Fatalf("%s: the page broke its rule: %v\n%s", scenario, err, out)
	}
}

// TestSubscribeFailureDropsTheDevice proves a subscribe POST that rejects, not
// only one that answers, is treated like a refusal: the device's subscription
// goes and the switch stays off.
func TestSubscribeFailureDropsTheDevice(t *testing.T) { notifyTap(t, "subscribe-rejected") }

// TestSubscribeFailureNamesAKeptSubscription proves the page does not pretend
// the device is off when both dm and the drop failed: the subscription is still
// there, so the banner says so instead of claiming a clean refusal.
func TestSubscribeFailureNamesAKeptSubscription(t *testing.T) {
	notifyTap(t, "subscribe-rejected-kept")
}

// TestUnsubscribeKeepsTheSwitchOnWhenTheDeviceRefuses proves a refused
// unsubscribe() leaves dm's row alone and the switch on. Dropping the row would
// lose a phone that still holds the subscription, and the next load would paint
// on for a phone dm could no longer reach.
func TestUnsubscribeKeepsTheSwitchOnWhenTheDeviceRefuses(t *testing.T) {
	notifyTap(t, "tap-off-refused")
}

// TestUnsubscribePostsAndRecordsOffWhenTheDeviceDrops proves the ordinary tap
// off still tells dm and records the device as off.
func TestUnsubscribePostsAndRecordsOffWhenTheDeviceDrops(t *testing.T) {
	notifyTap(t, "tap-off-dropped")
}
