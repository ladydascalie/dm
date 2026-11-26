package server

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net/url"
	"strings"
	"time"

	"dm/internal/box"
	"dm/internal/push"
)

const (
	// pushTimeout bounds one delivery run. The goroutine that sends never
	// holds s.mu, and a push service that accepts a request and then stalls
	// must not leave the goroutine around forever.
	pushTimeout = 15 * time.Second
	// pushMaxSubs caps how many phones one entry reaches. A session that
	// collected stale subscriptions must not turn one notify into a flood.
	pushMaxSubs = 8
	// pushMaxInFlight caps how many delivery runs exist at once. A notify
	// flood would otherwise start one goroutine per entry, each parked on a
	// push service. An entry that arrives while every slot is taken is
	// dropped: the box still has it, and the newest entry matters more than
	// the backlog.
	pushMaxInFlight = 8
	// pushTTL is how long a push service keeps an undelivered message. A pane
	// state is news for minutes, not a day, so a phone that was offline does
	// not wake to yesterday's block.
	pushTTL = time.Hour
	// pushUrgency is the RFC 8030 urgency. Only blocked and error reach this
	// renderer, so every push is worth waking the phone.
	pushUrgency = "high"
	// pushSubject is the VAPID sub claim: a contact URL for whoever runs dm.
	// The push service operator reads it; the phone never sees it.
	pushSubject = "https://github.com/ladydascalie/dm"
)

// pushSlots is the process-wide delivery budget. Two sessions in one process
// share it, which is the right unit: the cost is network and goroutines, not
// session state.
var pushSlots = make(chan struct{}, pushMaxInFlight)

// pushBoxEntryLocked renders one notify/ entry as a phone notification. It
// runs beside notify.SystemNotify and behind the same two gates, so the entry
// the operator is already looking at, or the state urgency rejects, never
// reaches a phone. The caller holds s.mu, so every slow step happens in a
// goroutine: resolving the session path and taking a slot are the only work
// done here.
func (s *server) pushBoxEntryLocked(e box.Entry) {
	pane := strings.TrimPrefix(e.Key, box.NotifyPrefix)
	sessionDir := s.st.SessionDir()
	select {
	case pushSlots <- struct{}{}:
	default:
		log.Printf("push: %d deliveries in flight, dropping one notification", cap(pushSlots))
		return
	}
	go func() {
		defer func() { <-pushSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
		defer cancel()
		pushEntry(ctx, sessionDir, pane, e)
	}()
}

// pushEntry delivers e to every phone subscribed to the session at sessionDir.
// It is the goroutine half: it reads the key pair and the store from disk, so
// no file work happens under s.mu.
//
// A missing key file or an empty store means no phone has subscribed yet, and
// that is not an error: dm web creates both when the page subscribes. Any
// other failure is logged. The push package already keeps the subscription
// endpoint and its secrets out of its errors, so the log never names either.
func pushEntry(ctx context.Context, sessionDir, pane string, e box.Entry) {
	keys, err := push.LoadKeys(push.KeysPath(sessionDir))
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		log.Printf("push: load keys: %v", err)
		return
	}
	store, err := push.Load(push.StorePath(sessionDir))
	if err != nil {
		log.Printf("push: load subscriptions: %v", err)
		return
	}
	subs := store.List()
	if len(subs) > pushMaxSubs {
		subs = subs[:pushMaxSubs]
	}
	sender := &push.Sender{
		Keys:    keys,
		Subject: pushSubject,
		TTL:     pushTTL,
		Urgency: pushUrgency,
	}
	msg := push.Message{
		Title: pane + " " + string(e.State),
		Body:  e.Msg,
		Pane:  pane,
		Tag:   e.Key,
	}
	for _, sub := range subs {
		if ctx.Err() != nil {
			return
		}
		result, err := sender.Send(ctx, sub, msg)
		switch {
		case err != nil:
			log.Printf("push: send: %s", redactEndpoint(err, sub.Endpoint))
		case result.Gone:
			// The push service discarded this subscription. Remove takes the
			// file lock and reloads first, so a phone that resubscribed while
			// this run was in flight keeps the entry it just wrote.
			if _, err := store.Remove(sub.Endpoint); err != nil {
				log.Printf("push: prune gone subscription: %v", err)
			}
		}
	}
}

// redactEndpoint strips the subscription endpoint's host from an error before
// it reaches the log. The push package drops the path already, because the
// path is the capability that reaches the phone. The host names the phone's
// push service, so it goes too: no part of an endpoint belongs in a log.
func redactEndpoint(err error, endpoint string) string {
	msg := err.Error()
	u, parseErr := url.Parse(endpoint)
	if parseErr != nil || u.Host == "" {
		return msg
	}
	return strings.ReplaceAll(msg, u.Host, "<push-service>")
}
