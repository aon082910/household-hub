package core

import "testing"

func TestListsChoresAllowanceMeals(t *testing.T) {
	s, mom, _ := setupFin(t) // 2026-11-25 (Wednesday)
	kid, _ := s.CreateUser("kid", "Kid", "password1", RoleKid, "")
	kid2, _ := s.CreateUser("kid2", "Kid 2", "password1", RoleKid, "")

	// lists: kids can use shared lists, not private ones
	shop := List{Name: "Groceries"}
	s.SaveList(mom.ID, &shop)
	priv := List{Name: "Gift ideas", Kind: "todo", Private: true}
	s.SaveList(mom.ID, &priv)
	items, err := s.AddItems(kid.ID, shop.ID, "2 milk\n- eggs\n\n bread ", nil, "")
	if err != nil || len(items) != 3 || items[0].Qty != "2" || items[0].Text != "milk" || items[1].Text != "eggs" {
		t.Fatalf("items %+v %v", items, err)
	}
	if _, err := s.AddItems(kid.ID, priv.ID, "x", nil, ""); err != ErrNotFound {
		t.Error("kid wrote to a private list")
	}
	s.SetItemDone(kid.ID, items[0].ID, true)
	l, _ := s.GetList(mom.ID, shop.ID)
	if l.Open != 2 || !l.Items[2].Done {
		t.Errorf("list %+v", l)
	}

	// chores
	c := Chore{Name: "Take out trash", AssignedTo: &kid.ID, Freq: "weekly", StartDate: "2026-11-23", RewardCents: 200}
	s.SaveChore(mom.ID, &c)
	if err := s.MarkChoreDone(kid2, c.ID, "2026-11-23"); err != ErrForbidden {
		t.Error("another kid ticked someone else's chore")
	}
	if err := s.MarkChoreDone(kid, c.ID, "2026-11-23"); err != nil {
		t.Fatal(err)
	}
	if bal, _, _ := s.Allowance(kid, kid.ID); bal != 0 {
		t.Error("paid before an adult approved")
	}
	due, _ := s.ChoresDue(at("2026-11-23"), at("2026-11-30"))
	var doneID int64
	for _, d := range due {
		if d.Date == "2026-11-23" && d.Status == "waiting" && d.DoneID != nil {
			doneID = *d.DoneID
		}
	}
	if doneID == 0 || len(due) != 2 {
		t.Fatalf("due %+v", due)
	}
	if err := s.DecideChore(kid, doneID, true); err != ErrForbidden {
		t.Error("kid approved a chore")
	}
	s.DecideChore(mom, doneID, true)
	if bal, _, _ := s.Allowance(kid, kid.ID); bal != 200 {
		t.Errorf("allowance %d", bal)
	}
	if _, _, err := s.Allowance(kid2, kid.ID); err != ErrForbidden {
		t.Error("kid saw another kid's allowance")
	}
	s.AddAllowance(mom, kid.ID, -150, "paid out")
	if bal, _, _ := s.Allowance(mom, kid.ID); bal != 50 {
		t.Errorf("after payout %d", bal)
	}
	if err := s.AddAllowance(kid, kid.ID, 1000, "free money"); err != ErrForbidden {
		t.Error("kid paid themself")
	}

	// meals -> shopping list (eggs already on it)
	s.SaveMeal(mom.ID, &Meal{Date: "2026-11-26", Title: "Tacos", Ingredients: "1 lb ground beef\ntortillas\neggs"})
	s.SaveMeal(mom.ID, &Meal{Date: "2026-11-27", Title: "Pancakes", Ingredients: "flour\n2 eggs"})
	n, err := s.MealsToList(mom.ID, shop.ID, "2026-11-26", "2026-12-02")
	if err != nil || n != 3 {
		t.Errorf("meals to list %d %v", n, err)
	}
}
