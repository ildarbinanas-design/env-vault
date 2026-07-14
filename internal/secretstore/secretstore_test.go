package secretstore

import "testing"

func TestValidateSecretNamePreservesSafeSlashHierarchy(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"token", "team/project-token", "registry.example/@scope/token"} {
		if err := ValidateSecretName(name); err != nil {
			t.Fatalf("ValidateSecretName(%q): %v", name, err)
		}
	}
}

func TestValidateSecretNameRejectsUnsafePaths(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"/absolute",
		"../outside",
		"team/../../outside",
		"team/./token",
		"team//token",
		"team/token/",
		".",
		"..",
	} {
		if err := ValidateSecretName(name); err == nil {
			t.Fatalf("ValidateSecretName(%q) unexpectedly succeeded", name)
		}
	}
}

func TestValidateServiceNameRejectsUnsafePaths(t *testing.T) {
	t.Parallel()
	for _, service := range []string{
		"/absolute",
		`\absolute`,
		"C:/absolute",
		"../outside",
		"team/../../outside",
		"team/./service",
		"team//service",
		"team/service/",
		".",
		"..",
	} {
		if err := ValidateServiceName(service); err == nil {
			t.Fatalf("ValidateServiceName(%q) unexpectedly succeeded", service)
		}
	}
	for _, service := range []string{"env-vault", "team/dev", "Example service"} {
		if err := ValidateServiceName(service); err != nil {
			t.Fatalf("ValidateServiceName(%q): %v", service, err)
		}
	}
}
