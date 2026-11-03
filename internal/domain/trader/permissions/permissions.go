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
	PlatformAdmin     Role = "PLATFORM_ADMIN"
)

const (
	ProfileRead          = "profile:read"
	CustomerRead         = "customer:read"
	CustomerCreate       = "customer:create"
	CustomerUpdate       = "customer:update"
	CustomerWrite        = "customer:write"
	KYCRead              = "kyc:read"
	KYCUpdate            = "kyc:update"
	KYCVerify            = "kyc:validate"
	KYCReassess          = "kyc:reassess"
	TransactionCreate    = "transaction:create"
	TransactionRead      = "transaction:read"
	RiskFlagCreate       = "riskflag:create"
	RiskFlagRead         = "riskflag:read"
	RiskFlagInvestigate  = "risk_flag:investigate"
	RiskFlagDelete       = "risk_flag:delete"
	DashboardRead        = "dashboard:read"
	TraderManage         = "trader:manage"
	AnalysisRead         = "analysis:read"
	StoreRead            = "store:read"
	StoreManageMembers   = "store:manage_members"
	PlatformManageStores = "platform:manage_stores"
	RiskFlagResolve      = "riskflag:resolve"
)

var rolePermissions = map[Role]map[string]struct{}{
	Trader:            set(ProfileRead, CustomerRead, CustomerCreate, CustomerUpdate, CustomerWrite, KYCRead, KYCUpdate, TransactionCreate, TransactionRead, RiskFlagCreate, RiskFlagRead, DashboardRead, AnalysisRead, StoreRead),
	ComplianceOfficer: set(ProfileRead, CustomerRead, CustomerCreate, CustomerUpdate, CustomerWrite, KYCRead, KYCUpdate, KYCVerify, KYCReassess, TransactionCreate, TransactionRead, RiskFlagCreate, RiskFlagRead, RiskFlagInvestigate, RiskFlagResolve, DashboardRead, AnalysisRead, StoreRead),
	Supervisor:        set(ProfileRead, CustomerRead, CustomerCreate, TransactionCreate, TransactionRead, RiskFlagCreate, RiskFlagRead, DashboardRead, AnalysisRead, StoreRead, StoreManageMembers),
	PlatformAdmin:     set(ProfileRead, PlatformManageStores, TraderManage),
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
	if role == Admin {
		return role, true
	} // accepted only for legacy migration; it grants no permissions
	_, ok := rolePermissions[role]
	return role, ok
}
func Has(role, permission string) bool {
	for _, item := range strings.Split(role, ",") {
		permissions, ok := rolePermissions[Role(strings.ToUpper(strings.TrimSpace(item)))]
		if _, allowed := permissions[permission]; ok && allowed {
			return true
		}
	}
	return false
}
func ForRole(role string) []string {
	resultSet := map[string]struct{}{}
	for _, item := range strings.Split(role, ",") {
		for permission := range rolePermissions[Role(strings.ToUpper(strings.TrimSpace(item)))] {
			resultSet[permission] = struct{}{}
		}
	}
	result := make([]string, 0, len(resultSet))
	for permission := range resultSet {
		result = append(result, permission)
	}
	sort.Strings(result)
	return result
}
func Roles() []Role { return []Role{Trader, ComplianceOfficer, Supervisor, Admin, PlatformAdmin} }
