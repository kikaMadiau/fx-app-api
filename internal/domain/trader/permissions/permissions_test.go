package permissions

import "testing"

func TestPermissionMatrix(t *testing.T) {
	matrix := map[string][]Role{
		ProfileRead:         {Trader, ComplianceOfficer, Supervisor, PlatformAdmin},
		CustomerRead:        {Trader, ComplianceOfficer, Supervisor},
		CustomerCreate:      {Trader, ComplianceOfficer, Supervisor},
		CustomerUpdate:      {Trader, ComplianceOfficer},
		KYCRead:             {Trader, ComplianceOfficer},
		KYCUpdate:           {Trader, ComplianceOfficer},
		KYCVerify:           {ComplianceOfficer},
		KYCReassess:         {ComplianceOfficer},
		TransactionCreate:   {Trader, ComplianceOfficer, Supervisor},
		TransactionRead:     {Trader, ComplianceOfficer, Supervisor},
		RiskFlagCreate:      {Trader, ComplianceOfficer, Supervisor},
		RiskFlagRead:        {Trader, ComplianceOfficer, Supervisor},
		RiskFlagInvestigate: {ComplianceOfficer},
		RiskFlagDelete:      {},
		DashboardRead:       {Trader, ComplianceOfficer, Supervisor},
		TraderManage:        {PlatformAdmin},
		AnalysisRead:        {Trader, ComplianceOfficer, Supervisor},
	}
	for permission, allowedRoles := range matrix {
		allowed := map[Role]bool{}
		for _, role := range allowedRoles {
			allowed[role] = true
		}
		for _, role := range Roles() {
			if got := Has(string(role), permission); got != allowed[role] {
				t.Errorf("Has(%s, %s) = %v, want %v", role, permission, got, allowed[role])
			}
		}
		if Has("UNRECOGNIZED", permission) {
			t.Errorf("unknown role has %s", permission)
		}
	}
}
func TestNormalizeRole(t *testing.T) {
	for _, role := range []string{"TRADER", "COMPLIANCE_OFFICER", "SUPERVISOR", "ADMIN"} {
		if _, ok := NormalizeRole(role); !ok {
			t.Errorf("NormalizeRole(%q) rejected", role)
		}
	}
	for _, role := range []string{"MANAGER", "admin,trader", "UNKNOWN"} {
		if _, ok := NormalizeRole(role); ok {
			t.Errorf("NormalizeRole(%q) accepted", role)
		}
	}
}
