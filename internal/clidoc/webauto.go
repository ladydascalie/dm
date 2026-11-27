package clidoc

// WebNote says who owns the web listener and how the operator reaches it: the
// daemon starts it from the web-auto node, dm web reuses it instead of binding a
// second one, and the web-url key shows the link. The behaviour lives in
// internal/server, internal/webui and internal/client; this is the
// operator-facing copy of it, and the man page, the HTML page and llms.txt
// render it so no one of them claims a listener the daemon does not own.
const WebNote = "The web listener belongs to the session daemon. The web-auto node set to true starts it with the server, on web-addr, with the token web-token pins or the one the session keeps in web.token. A dm web with no --addr and no --token then prints the URL the daemon already serves instead of binding a second listener on the same address; an explicit flag still binds its own. Before the first attach there is no daemon, so dm web binds one as it always did. The web-url key opens a temporary float with that URL and its QR code under it, and closes on esc, q or the key again."
