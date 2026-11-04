package permissions

import "testing"

func TestPermissionMatrix(t *testing.T) {
	matrix := map[string][]Role{
		ProfileRead:         {Trader, ComplianceOfficer, Supervisor, PlatformAdmin},
		ProfileUpdate:       {Trader, ComplianceOfficer, Supervisor, PlatformAdmin},
		CustomerRead:        {Trader, ComplianceOfficer, Supervisor, PlatformAdmin},
		CustomerCreate:      {Trader, ComplianceOfficer, Supervisor, PlatformAdmin},
		CustomerUpdate:      {Trader, ComplianceOfficer, PlatformAdmin},
		KYCRead:             {Trader, ComplianceOfficer, PlatformAdmin},
		KYCUpdate:           {Trader, ComplianceOfficer, PlatformAdmin},
		KYCVerify:           {ComplianceOfficer},
		KYCReassess:         {ComplianceOfficer, PlatformAdmin},
		TransactionCreate:   {Trader, ComplianceOfficer, Supervisor, PlatformAdmin},
		TransactionRead:     {Trader, ComplianceOfficer, Supervisor, PlatformAdmin},
		RiskFlagCreate:      {Trader, ComplianceOfficer, Supervisor},
		RiskFlagRead:        {Trader, ComplianceOfficer, Supervisor, PlatformAdmin},
		RiskFlagInvestigate: {ComplianceOfficer},
		RiskFlagDelete:      {PlatformAdmin},
		DashboardRead:       {Trader, ComplianceOfficer, Supervisor, PlatformAdmin},
		TraderManage:        {PlatformAdmin},
		AnalysisRead:        {Trader, ComplianceOfficer, Supervisor, PlatformAdmin},
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
