// Package demoroute holds the integration evidence for the demo template's
// required route node (GitHub issue #314).
//
// The demo template version 2 declares one RouteNode as required. The
// declaration is a template rule, so the required flag of that node is
// computed by the server, and the acceptance criteria AC-G3-08 / AC-G3-16
// become runnable: a missing route node, a route node that has not been
// reviewed, and a route node that is behind its baseline each produce a
// blocking validation issue and keep the revision from being stabilized.
//
// The package owns no production code. It exists so the evidence for the
// declaration lives next to the declaration instead of inside the modules
// this change must not touch.
package demoroute
