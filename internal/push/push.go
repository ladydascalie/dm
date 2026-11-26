// Package push is the standard-library half of Web Push: the VAPID key pair,
// the subscription store, the RFC 8291 payload encryption and the RFC 8292
// request headers.
//
// The package imports nothing from the module, so it sits in the leaf layer.
// The web surface wires it together: load the VAPID keys once, keep the
// subscriptions in a Store, and hand a Sender one subscription and one Message
// per delivery. A push service answer that says the subscription is gone is a
// value, not an error, so the caller can prune the phone it can no longer
// reach.
package push

import "time"

// Subscription is one phone's push subscription, as the browser reported it.
//
// The JSON field names are the wire contract: encounter two stores and serves
// them unchanged. Endpoint is a capability URL, P256DH and Auth are the phone's
// public key and auth secret; all three are secrets, none of them belongs in a
// log or an error string.
type Subscription struct {
	Endpoint string    `json:"endpoint"`
	P256DH   string    `json:"p256dh"`
	Auth     string    `json:"auth"`
	At       time.Time `json:"at"`
	Label    string    `json:"label,omitempty"`
}

// Message is the payload the service worker parses. Its JSON field names are
// the whole contract with the browser.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	Pane  string `json:"pane,omitempty"`
	Tag   string `json:"tag,omitempty"`
	URL   string `json:"url,omitempty"`
}
