package workspace

import "github.com/gin-gonic/gin"

// Transport-only registration. Application contracts in contracts.go do not
// import Gin; the main router attaches guards before passing these groups.
type RouteRegistrar interface {
	GET(string, ...gin.HandlerFunc) gin.IRoutes
	POST(string, ...gin.HandlerFunc) gin.IRoutes
	PUT(string, ...gin.HandlerFunc) gin.IRoutes
}
type RouteGroups struct{ Read, Write RouteRegistrar }
