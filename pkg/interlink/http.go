package interlink

// IsSuccessStatus reports whether an HTTP status code from a plugin or from the
// interLink API is a success.
//
// Any 2xx counts. The checks used to compare against 200 exactly, which turned
// the other legal success codes into hard failures: a plugin answering 201
// Created to /create, or 204 No Content to /delete, had its response rejected,
// an error body written over it, and — once spans carry an outcome — the call
// recorded as failed. Codes outside 2xx stay failures, including the 1xx and
// 3xx that the plugin protocol never uses.
func IsSuccessStatus(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}
