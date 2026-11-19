// SPDX-License-Identifier: MIT

// Package wsbearer is the API front's WebSocket bearer carrier: the
// outermost stage of the request pipeline, which promotes a bearer a
// browser sent in the WebSocket sub-protocol header to the standard
// Authorization header and strips the credential from the sub-protocol
// header before any other stage, the access log and the authentication
// middleware among them, reads the request.
//
// spec: GWAPI, PLAY
package wsbearer
