package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/peragolabs/spendmux/internal/database"
	"github.com/peragolabs/spendmux/models"

	"github.com/google/uuid"
)

// CreatePersonalOrg creates a personal workspace owned by userID and sets it as
// the user's default_org_id. Returns the new org id.
func CreatePersonalOrg(userID uuid.UUID, displayName string) (uuid.UUID, error) {
	trimmed := strings.TrimSpace(displayName)
	if trimmed == "" {
		trimmed = "My"
	}
	name := trimmed + " Workspace"
	org := models.Org{
		Name:       name,
		IsPersonal: true,
		CreatedBy:  userID,
	}
	db := database.Get()
	if err := db.Create(&org).Error; err != nil {
		return uuid.Nil, err
	}
	member := models.OrgMember{
		OrgID:  org.ID,
		UserID: userID,
		Role:   "owner",
	}
	if err := db.Create(&member).Error; err != nil {
		return uuid.Nil, err
	}
	if err := db.Model(&models.User{}).Where("id = ?", userID).Update("default_org_id", org.ID).Error; err != nil {
		return uuid.Nil, err
	}
	return org.ID, nil
}

// GetMembership returns the caller's role in the org, or ("", false) if not a member.
func GetMembership(orgID, userID uuid.UUID) (string, bool) {
	var member models.OrgMember
	err := database.Get().Where("org_id = ? AND user_id = ?", orgID, userID).First(&member).Error
	if err != nil {
		return "", false
	}
	return member.Role, member.Role != ""
}

// RequireRole returns the caller's role if it is in allowed, else ("", false).
func RequireRole(orgID, userID uuid.UUID, allowed ...string) (string, bool) {
	role, ok := GetMembership(orgID, userID)
	if !ok {
		return "", false
	}
	for _, a := range allowed {
		if role == a {
			return role, true
		}
	}
	return "", false
}

// Slugify produces an invite/deep-link-safe slug from a workspace name.
func Slugify(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteRune('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	if slug == "" {
		return "team"
	}
	return slug
}

// GenerateUniqueSlug returns a slug not already used by another org.
func GenerateUniqueSlug(name string) string {
	base := Slugify(name)
	candidate := base
	db := database.Get()
	for attempt := 0; attempt < 5; attempt++ {
		var count int64
		db.Model(&models.Org{}).Where("slug = ?", candidate).Count(&count)
		if count == 0 {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%s", base, uuid.New().String()[:6])
	}
	return fmt.Sprintf("%s-%s", base, uuid.New().String()[:8])
}

// LoadUser returns a user by ID (respects soft-delete).
func LoadUser(id uuid.UUID) (*models.User, error) {
	var user models.User
	if err := database.Get().First(&user, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// RoleForEmail returns "admin" when email is in ADMIN_EMAILS, else "user".
func RoleForEmail(email string) string {
	if IsAdminEmail(email) {
		return "admin"
	}
	return "user"
}

// TouchUserUpdated bumps UpdatedAt.
func TouchUserUpdated(id uuid.UUID) {
	_ = database.Get().Model(&models.User{}).Where("id = ?", id).Update("updated_at", time.Now().UTC()).Error
}
