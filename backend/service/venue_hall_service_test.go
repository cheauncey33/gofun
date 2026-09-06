package service

import "testing"

func TestValidatePhysicalLayout(t *testing.T) {
	valid := HallLayoutInput{Name: "一号厅 V1", RowCount: 2, ColCount: 3, Seats: []PhysicalSeatInput{
		{RowNo: 1, ColNo: 1, Label: "A1", ZoneKey: "vip"},
		{RowNo: 2, ColNo: 1, Label: "B1", ZoneKey: "general"},
	}}
	if err := validatePhysicalLayout(valid); err != nil {
		t.Fatalf("valid layout rejected: %v", err)
	}

	duplicate := valid
	duplicate.Seats = append(duplicate.Seats, PhysicalSeatInput{RowNo: 1, ColNo: 1})
	if err := validatePhysicalLayout(duplicate); err == nil {
		t.Fatal("duplicate cell should be rejected")
	}

	outOfBounds := valid
	outOfBounds.Seats = []PhysicalSeatInput{{RowNo: 3, ColNo: 1}}
	if err := validatePhysicalLayout(outOfBounds); err == nil {
		t.Fatal("out-of-bounds cell should be rejected")
	}
}

func TestPhysicalSeatsDefaultZone(t *testing.T) {
	rows := physicalSeats(9, []PhysicalSeatInput{{RowNo: 1, ColNo: 1, Label: ""}})
	if len(rows) != 1 || rows[0].LayoutID != 9 || rows[0].ZoneKey != "general" || rows[0].Label != "A1" {
		t.Fatalf("unexpected physical seat: %#v", rows)
	}
	if rows[0].TicketTierID != nil {
		t.Fatal("physical seat must not bind an event ticket tier")
	}
}
