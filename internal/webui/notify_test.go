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

// world is what the scenario set up and what the page did to it. attempts
// records every request the page started, posts only those dm answered.
const world = {
  refusesDrop: false,
  postFails: false,
  attempts: [],
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

// fetchAPI answers what the scenario needs, and records every request the page
// makes of dm.
async function fetchAPI(path) {
  world.attempts.push(path);
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

// notifyFunctions is every function a tap or a load reaches, in the page's own
// source.
var notifyFunctions = []string{
	"notifySupported", "paintNotify", "setStoredNotify",
	"swRegistration", "loadNotify", "b64url", "fromB64url", "dropSubscription",
	"postSubscription", "subscribePush", "unsubscribePush", "toggleNotify",
}

// notifyChecks is one tap or one load per scenario, and the facts it must
// leave behind.
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
  // The POST rejects and the device refuses the drop. The tap cannot repair it,
  // so the switch must be off rather than claim a phone dm has no row for. The
  // reload is where it repairs: dm answers, so the switch records on and dm
  // ends up with the row.
  world.postFails = true;
  world.refusesDrop = true;
  await toggleNotify();
  want(device.drops === 1, "device drops = " + device.drops + ", want 1");
  want(notifyOn === false, "notifyOn = " + notifyOn + ", want false: dm has no row");
  want(button.dataset.state === "off", "the button reads " + button.dataset.state + ", want off");
  want(world.banners.some((line) => line.includes("kept the subscription")), "banners = " + JSON.stringify(world.banners) + ", want the kept subscription named");

  world.postFails = false;
  world.attempts.length = 0;
  world.posts.length = 0;
  await loadNotify();
  want(world.posts.includes("/api/push/subscribe"), "posts = " + JSON.stringify(world.posts) + ", want the reload to hand the device's subscription to dm");
  want(notifyOn === true, "afterReload notifyOn = " + notifyOn + ", want true: dm took the row");
  want(memory.get(NOTIFY_KEY) === "on", "afterReload stored = " + memory.get(NOTIFY_KEY) + ", want on");
  want(button.dataset.state === "on", "afterReload the button reads " + button.dataset.state + ", want on");
} else if (scenario === "reload-post-fails") {
  // The device holds a subscription and dm does not take it: the switch records
  // off, because dm has no row and can deliver nothing.
  notifyOn = true;
  memory.set(NOTIFY_KEY, "on");
  world.postFails = true;
  await loadNotify();
  want(world.attempts.includes("/api/push/subscribe"), "attempts = " + JSON.stringify(world.attempts) + ", want the load to try dm");
  want(notifyOn === false, "notifyOn = " + notifyOn + ", want false");
  want(memory.get(NOTIFY_KEY) === "off", "stored = " + memory.get(NOTIFY_KEY) + ", want off");
  want(button.dataset.state === "off", "the button reads " + button.dataset.state + ", want off");
} else if (scenario === "reload-denied") {
  // Permission refused: the device's subscription cannot show a notification,
  // so the load writes nothing.
  Notification.permission = "denied";
  world.posts.length = 0;
  world.attempts.length = 0;
  await loadNotify();
  want(world.attempts.length === 0, "attempts = " + JSON.stringify(world.attempts) + ", want none under a refused permission");
  want(notifyOn === false, "notifyOn = " + notifyOn + ", want false");
  want(memory.get(NOTIFY_KEY) === "off", "stored = " + memory.get(NOTIFY_KEY) + ", want off");
  want(button.dataset.state === "denied", "the button reads " + button.dataset.state + ", want denied");
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

// TestSubscribeFailureNamesAKeptSubscription proves the honest reading of a tap
// where dm refused the POST and the device refused the drop: the switch is off,
// because dm has no row. The next load hands the kept subscription to dm, and
// the switch records on only once dm has the row.
func TestSubscribeFailureNamesAKeptSubscription(t *testing.T) {
	notifyTap(t, "subscribe-rejected-kept")
}

// TestReloadRecordsOffWhenThePostFails proves the mirror: a device that holds a
// subscription dm will not take reads off, so the switch never claims a phone dm
// cannot reach.
func TestReloadRecordsOffWhenThePostFails(t *testing.T) { notifyTap(t, "reload-post-fails") }

// TestReloadPostsNothingWhenPermissionIsDenied proves a refused permission
// writes nothing to dm: a subscription under it can show no notification.
func TestReloadPostsNothingWhenPermissionIsDenied(t *testing.T) { notifyTap(t, "reload-denied") }

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
