package domain

import "time"

type SparkUITicket struct {
	TokenHash, SessionHash, Namespace, Application, TenantID string
	ExpiresAt                                                time.Time
}
