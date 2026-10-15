package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const maxResponseBytes = 4 << 20

type resolveRequest struct {
	NS         string `json:"ns" binding:"required"`
	Domain     string `json:"domain" binding:"required"`
	SourceIP   string `json:"source_ip"`
	RecordType string `json:"record_type"`
}

type routeRequest struct {
	Domain     string `json:"domain"`
	SourceIP   string `json:"source_ip,omitempty"`
	RecordType string `json:"record_type,omitempty"`
}

func (m *Module) RegisterRoutes(r *gin.Engine) {
	group := r.Group("/utils", m.middlewares...)
	group.POST("/resolve", m.resolve)
}

func (m *Module) resolve(c *gin.Context) {
	var input resolveRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	endpoint, err := url.Parse(strings.TrimSpace(input.NS))
	if err != nil || endpoint.Hostname() == "" ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.User != nil || endpoint.Fragment != "" || endpoint.Opaque != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ns must be a full HTTP or HTTPS URL without credentials or a fragment"})
		return
	}
	input.Domain = strings.TrimSpace(input.Domain)
	if input.Domain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain is required"})
		return
	}
	input.SourceIP = strings.TrimSpace(input.SourceIP)
	if input.SourceIP != "" && net.ParseIP(input.SourceIP) == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source_ip must be an IPv4 or IPv6 address"})
		return
	}

	input.RecordType = strings.ToUpper(strings.TrimSpace(input.RecordType))
	switch input.RecordType {
	case "", "A", "AAAA", "CNAME", "TXT", "MX", "SRV", "NS":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "record_type must be A, AAAA, CNAME, TXT, MX, SRV, or NS"})
		return
	}
	payload, err := json.Marshal(routeRequest{Domain: input.Domain, SourceIP: input.SourceIP, RecordType: input.RecordType})
	if err != nil {
		logger.L().Error("Failed to encode DNS route request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode route request"})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid ns endpoint"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		logger.L().Warn("DNS router request failed", zap.Error(err))
		status := http.StatusBadGateway
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			status = http.StatusGatewayTimeout
		}
		c.JSON(status, gin.H{"error": "failed to contact DNS router"})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		logger.L().Warn("Failed to read DNS router response", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read DNS router response"})
		return
	}
	if len(body) > maxResponseBytes {
		c.JSON(http.StatusBadGateway, gin.H{"error": "DNS router response exceeds size limit"})
		return
	}
	body = bytes.TrimSpace(body)
	if !json.Valid(body) || len(body) == 0 || body[0] != '{' ||
		resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		c.JSON(http.StatusBadGateway, gin.H{"error": "DNS router returned an invalid JSON object or unexpected status"})
		return
	}
	c.Data(resp.StatusCode, "application/json", body)
}
