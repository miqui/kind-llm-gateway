package tenants

// Tenant represents a registered API consumer with policy attributes.
type Tenant struct {
	ID              string
	Name            string
	APIKey          string
	RateLimitRPS    float64
	DailyTokenQuota int64
	AllowedModels   []string
	WorkloadClass   string
	CostTier        string
}
