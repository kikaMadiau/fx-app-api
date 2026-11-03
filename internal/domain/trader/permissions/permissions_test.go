package permissions

import "testing"

func TestPermissionMatrix(t *testing.T) {
	matrix := map[string][]Role{
		ProfileRead:         {Trader, ComplianceOfficer, Supervisor, Admin},
		CustomerRead:        {Trader, ComplianceOfficer, Supervisor, Admin},
		CustomerCreate:      {Trader, ComplianceOfficer, Supervisor, Admin},
		CustomerUpdate:      {Trader, ComplianceOfficer, Admin},
		KYCRead:             {Trader, ComplianceOfficer, Admin},
		KYCUpdate:           {Trader, ComplianceOfficer, Admin},
		KYCVerify:           {ComplianceOfficer},
		KYCReassess:         {ComplianceOfficer, Admin},
		TransactionCreate:   {Trader, ComplianceOfficer, Supervisor, Admin},
		TransactionRead:     {Trader, ComplianceOfficer, Supervisor, Admin},
		RiskFlagCreate:      {Trader, ComplianceOfficer, Supervisor},
		RiskFlagRead:        {Trader, ComplianceOfficer, Supervisor, Admin},
		RiskFlagInvestigate: {ComplianceOfficer},
		RiskFlagDelete:      {Admin},
		DashboardRead:       {Trader, ComplianceOfficer, Supervisor, Admin},
		TraderManage:        {Admin},
		AnalysisRead:        {Trader, ComplianceOfficer, Supervisor, Admin},
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
