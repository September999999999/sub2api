package routes

import (
	"log"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterNotificationRoutes 注册通知配置路由
func RegisterNotificationRoutes(router *gin.RouterGroup, handler *admin.NotificationHandler) {
	webhook := router.Group("/webhook")
	webhook.Use(notificationAuditLogger())
	{
		webhook.GET("/config", handler.GetConfig)
		webhook.PUT("/config", handler.UpdateConfig)
		webhook.GET("/platforms", handler.GetPlatforms)
		webhook.POST("/platforms", handler.AddPlatform)
		webhook.PUT("/platforms/:id", handler.UpdatePlatform)
		webhook.DELETE("/platforms/:id", handler.DeletePlatform)
		webhook.POST("/platforms/:id/toggle", handler.TogglePlatform)
		webhook.POST("/platforms/:id/test", handler.TestPlatform)
		webhook.POST("/test", handler.ValidateConfig)
		webhook.POST("/test-notification", handler.SendTestNotification)
		webhook.POST("/send", handler.SendNotification)
	}
}

func notificationAuditLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		subject, _ := middleware.GetAuthSubjectFromContext(c)
		role, _ := middleware.GetUserRoleFromContext(c)
		authMethod := c.GetString("auth_method")
		latency := time.Since(start)

		method := ""
		path := ""
		if c.Request != nil {
			method = c.Request.Method
			if c.Request.URL != nil {
				path = c.Request.URL.Path
			}
		}
		fullPath := c.FullPath()
		if fullPath == "" {
			fullPath = path
		}

		log.Printf(
			"AUDIT: scope=admin_webhook method=%s path=%s status=%d user_id=%d role=%s auth_method=%s client_ip=%s latency_ms=%d",
			method,
			fullPath,
			c.Writer.Status(),
			subject.UserID,
			role,
			authMethod,
			c.ClientIP(),
			latency.Milliseconds(),
		)
	}
}
