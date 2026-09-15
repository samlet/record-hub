// Package modules documents the service's modular-monolith boundaries.
//
// Business capabilities live in cohesive child packages under modules. They
// depend on ports owned by the capability rather than directly on MongoDB,
// NATS, Dex, or another capability's implementation.
package modules
