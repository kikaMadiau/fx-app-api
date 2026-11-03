package permissions

import (
	"sort"
	"strings"
)

type Role string

const (
	Trader            Role = "TRADER"
	ComplianceOfficer Role = "COMPLIANCE_OFFICER"
	Supervisor        Role = "SUPERVISOR"
	Admin             Role = "ADMIN"
)

const (
	ProfileRead         = "profile:read"
	CustomerRead        = "customer:read"
	CustomerCreate      = "customer:create"
	CustomerUpdate      = "customer:update"
	KYCRead             = "kyc:read"
	KYCUpdate           = "kyc:update"
	KYCVerify           = "kyc:verify"
	KYCReassess         = "kyc:reassess"
	TransactionCreate   = "transaction:create"
	TransactionRead     = "transaction:read"
	RiskFlagCreate      = "risk_flag:create"
	RiskFlagRead        = "risk_flag:read"
	RiskFlagInvestigate = "risk_flag:investigate"
	RiskFlagDelete      = "risk_flag:delete"
	DashboardRead       = "dashboard:read"
	TraderManage        = "trader:manage"
	AnalysisRead        = "analysis:read"
)

var rolePermissions = map[Role]map[string]struct{}{
	Trader:            set(ProfileRead, CustomerRead, CustomerCreate, CustomerUpdate, KYCRead, KYCUpdate, TransactionCreate, TransactionRead, RiskFlagCreate, RiskFlagRead, DashboardRead, AnalysisRead),
	ComplianceOfficer: set(ProfileRead, CustomerRead, CustomerCreate, CustomerUpdate, KYCRead, KYCUpdate, KYCVerify, KYCReassess, TransactionCreate, TransactionRead, RiskFlagCreate, RiskFlagRead, RiskFlagInvestigate, DashboardRead, AnalysisRead),
	Supervisor:        set(ProfileRead, CustomerRead, CustomerCreate, TransactionCreate, TransactionRead, RiskFlagCreate, RiskFlagRead, DashboardRead, AnalysisRead),
	Admin:             set(ProfileRead, CustomerRead, CustomerCreate, CustomerUpdate, KYCRead, KYCUpdate, KYCReassess, TransactionCreate, TransactionRead, RiskFlagRead, RiskFlagDelete, DashboardRead, TraderManage, AnalysisRead),
}

func set(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
func NormalizeRole(value string) (Role, bool) {
	role := Role(strings.ToUpper(strings.TrimSpace(value)))
	_, ok := rolePermissions[role]
	return role, ok
}
func Has(role, permission string) bool {
	permissions, ok := rolePermissions[Role(strings.ToUpper(strings.TrimSpace(role)))]
	if !ok {
		return false
	}
	_, ok = permissions[permission]
	return ok
}
func ForRole(role string) []string {
	permissions, ok := rolePermissions[Role(strings.ToUpper(strings.TrimSpace(role)))]
	if !ok {
		return []string{}
	}
	result := make([]string, 0, len(permissions))
	for permission := range permissions {
		result = append(result, permission)
	}
	sort.Strings(result)
	return result
}
func Roles() []Role { return []Role{Trader, ComplianceOfficer, Supervisor, Admin} }
