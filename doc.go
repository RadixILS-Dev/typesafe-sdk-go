// Package typesafe provides a Go client for the TypeSafe AI System One API.
//
// Create a client with NewClient, then ask named Noul, Choice, and Score questions
// with Client.SystemOne. Clients may be shared by concurrent goroutines. Each
// call accepts a context that controls the entire operation, including retries.
package typesafe
