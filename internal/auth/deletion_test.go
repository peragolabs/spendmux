package auth

import (
	"errors"
	"github.com/google/uuid"
	"github.com/peragolabs/spendmux/internal/testdb"
	"github.com/peragolabs/spendmux/models"
	"gorm.io/gorm"
	"testing"
)

func deletionUser(t *testing.T) models.User {
	t.Helper()
	db := testdb.Setup(t)
	id := uuid.NewString()
	user := models.User{Username: id, ProviderID: id, Email: id + "@example.invalid"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func TestDeletionPurgesCache(t *testing.T) {
	user := deletionUser(t)
	db := testdb.Setup(t)
	if err := db.Create(&models.ResponseCache{ID: uuid.NewString(), UserID: user.ID, Ciphertext: []byte("encrypted")}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return deleteAccountData(tx, user.ID) }); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Unscoped().Model(&models.ResponseCache{}).Where("user_id = ?", user.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("retained data: %d %v", count, err)
	}
	if err := db.First(&models.User{}, "id = ?", user.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("user not deleted: %v", err)
	}
}

func TestDeletionPreservesSharedOrganizations(t *testing.T) {
	user := deletionUser(t)
	other := deletionUser(t)
	db := testdb.Setup(t)
	org := models.Org{Name: "Shared", CreatedBy: user.ID}
	if err := db.Create(&org).Error; err != nil {
		t.Fatal(err)
	}
	member := models.OrgMember{OrgID: org.ID, UserID: other.ID, Role: "member"}
	if err := db.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	err := db.Transaction(func(tx *gorm.DB) error { return deleteAccountData(tx, user.ID) })
	if !errors.Is(err, errSharedOrganization) {
		t.Fatalf("got %v", err)
	}
	if err := db.First(&models.Org{}, "id = ?", org.ID).Error; err != nil {
		t.Fatal("shared org deleted")
	}
	if err := db.First(&models.User{}, "id = ?", user.ID).Error; err != nil {
		t.Fatal("user deleted")
	}
}
