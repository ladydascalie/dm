// dm web: the service worker. It is here for one reason: a phone only grants
// push to an app it installed on the home screen, and an installed app needs a
// worker.
//
// It has no fetch handler and it caches nothing. Every page and every asset is
// fetched from the server on each load, because a phone holding yesterday's
// app.js is the bug this repository already avoids.

// install and activate take the new worker live at once: a phone that opened
// the page once must not wait for a second visit before push works.
self.addEventListener("install", () => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

// push shows one notification for the message dm sent: {title, body, pane, tag,
// url}. The tag defaults to the pane, so a second notice about the same pane
// replaces the first instead of stacking on the lock screen. The body is
// optional: a notice with no message is still worth a line.
self.addEventListener("push", (event) => {
  let msg = {};
  try {
    msg = event.data ? event.data.json() : {};
  } catch (e) {
    // A payload that is not JSON is not a notification: show the quiet title
    // rather than throw inside the push handler.
    msg = {};
  }
  const pane = msg.pane || "";
  event.waitUntil(
    self.registration.showNotification(msg.title || "dm", {
      body: msg.body || "",
      tag: msg.tag || pane || "dm",
      data: { pane: pane, url: msg.url || "" },
    })
  );
});

// notificationclick brings a page forward on the pane the notice named, or
// opens one, and closes the notice. A notice with no pane opens the fleet.
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const data = event.notification.data || {};
  const path = data.url || (data.pane ? "/#panes&pane=" + encodeURIComponent(data.pane) : "/");
  const target = new URL(path, self.location.origin).href;
  event.waitUntil(
    (async () => {
      const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      for (const client of windows) {
        if (new URL(client.url).origin !== self.location.origin) continue;
        await client.focus();
        if (client.navigate) await client.navigate(target);
        return;
      }
      await self.clients.openWindow(target);
    })()
  );
});
