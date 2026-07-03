// Package link connects the runner to the hosted control plane: the device
// flow behind `wasa login`, the stored credential, and the background dial
// loop that keeps a websocket to the api alive. The control plane is a layer
// — an unreachable api never breaks local wasa usage.
package link
