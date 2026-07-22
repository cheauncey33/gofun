package container

import (
	"WHU_Snack_GO/models"
	"reflect"
	"testing"
)

func TestTicketingSchemaModelsExcludeLegacyCommerceTables(t *testing.T) {
	modelsToMigrate := ticketingSchemaModels()
	actual := make(map[reflect.Type]bool, len(modelsToMigrate))
	for _, model := range modelsToMigrate {
		actual[reflect.TypeOf(model)] = true
	}

	required := []interface{}{
		&models.User{},
		&models.Organizer{},
		&models.Event{},
		&models.TicketOrder{},
		&models.TicketOrderOutbox{},
		&models.AdmissionTicket{},
		&models.TicketVerificationRecord{},
		&models.EventComment{},
	}
	for _, model := range required {
		if !actual[reflect.TypeOf(model)] {
			t.Fatalf("missing required ticketing model %T", model)
		}
	}

	legacy := []interface{}{
		&models.Product{},
		&models.Order{},
		&models.SeckillActivity{},
	}
	for _, model := range legacy {
		if actual[reflect.TypeOf(model)] {
			t.Fatalf("legacy commerce model %T must not be auto-migrated", model)
		}
	}
}
