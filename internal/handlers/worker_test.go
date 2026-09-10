package handlers

import (
	"testing"

	"bitebox/internal/db"
	"bitebox/internal/models"
)

// TestAuthorizeDepartmentMutationBoundary covers the gap TODO.md flagged as
// accepted-for-now and this session closed: department filtering used to
// be read-side only, so a bar worker who knew/guessed a kitchen order's id
// could still mutate it directly.
func TestAuthorizeDepartmentMutationBoundary(t *testing.T) {
	if _, err := db.CreateCategory("Test Mutation Kitchen", models.DepartmentKitchen); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	productID, err := db.CreateProduct("Test Mutation Steak", 12.0, -1, "Test Mutation Kitchen", "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if err := db.CreateTable(9101); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	orderID, err := db.CreateOrder(9101, "session", models.PaymentMethodCash, models.PaymentStatusUnpaid, "", 12.0, 0, 1,
		[]models.OrderItem{{ProductID: productID, Name: "Test Mutation Steak", Price: 12.0, Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	barWorker := models.User{Role: models.RoleWorker, Department: models.DepartmentBar}
	if authorizeDepartmentMutation(barWorker, orderID) {
		t.Error("a bar worker was authorized to mutate a kitchen-only order — this is exactly the gap the check exists to close")
	}

	kitchenWorker := models.User{Role: models.RoleWorker, Department: models.DepartmentKitchen}
	if !authorizeDepartmentMutation(kitchenWorker, orderID) {
		t.Error("a kitchen worker was denied their own department's order")
	}

	admin := models.User{Role: models.RoleAdmin}
	if !authorizeDepartmentMutation(admin, orderID) {
		t.Error("admin should bypass the department boundary entirely")
	}

	waiter := models.User{Role: models.RoleWorker, Department: models.DepartmentWaiter}
	if !authorizeDepartmentMutation(waiter, orderID) {
		t.Error("waiter should be unrestricted (cross-category ready-handoff is their whole job)")
	}

	superworker := models.User{Role: models.RoleWorker, Department: models.DepartmentSuperworker}
	if !authorizeDepartmentMutation(superworker, orderID) {
		t.Error("superworker (manager) should be unrestricted")
	}
}
