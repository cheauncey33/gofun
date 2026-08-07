package container

import (
	"WHU_Snack_GO/models"
	"reflect"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
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

}

func TestRabbitQueueArgs(t *testing.T) {
	base := amqp.Table{"x-dead-letter-exchange": "orders.dlx"}
	if got := rabbitQueueArgs("classic", base); !reflect.DeepEqual(got, base) {
		t.Fatalf("classic queue args changed: %#v", got)
	}

	got := rabbitQueueArgs("quorum", base)
	if got["x-queue-type"] != "quorum" {
		t.Fatalf("missing quorum queue type: %#v", got)
	}
	if got["x-dead-letter-exchange"] != "orders.dlx" {
		t.Fatalf("base queue args lost: %#v", got)
	}
}
