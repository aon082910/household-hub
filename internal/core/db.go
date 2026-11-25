// Package core holds the data model, storage, recurrence and reminder logic of Household Hub.
// Everything is in one SQLite file. Money is stored as integer cents.
package core

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the database.
type Store struct {
	DB   *sql.DB
	Path string
	mu   sync.Mutex // serialises writes that read-then-write
	Now  func() time.Time
}

var migrations = []string{
	// 1: base schema
	`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		username TEXT NOT NULL UNIQUE COLLATE NOCASE,
		display_name TEXT NOT NULL,
		color TEXT NOT NULL DEFAULT '#4f7cff',
		role TEXT NOT NULL DEFAULT 'adult',
		pass_hash TEXT NOT NULL,
		email TEXT NOT NULL DEFAULT '',
		phone TEXT NOT NULL DEFAULT '',
		sms_gateway TEXT NOT NULL DEFAULT '',
		ntfy_topic TEXT NOT NULL DEFAULT '',
		webhook_url TEXT NOT NULL DEFAULT '',
		channels TEXT NOT NULL DEFAULT 'inapp,device',
		ics_token TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1,
		created TEXT NOT NULL
	);
	CREATE TABLE sessions (
		token_hash TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		kind TEXT NOT NULL DEFAULT 'web',
		label TEXT NOT NULL DEFAULT '',
		created TEXT NOT NULL,
		expires TEXT NOT NULL,
		last_seen TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	CREATE TABLE accounts (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		type TEXT NOT NULL DEFAULT 'checking',
		opening_cents INTEGER NOT NULL DEFAULT 0,
		owner_id INTEGER NOT NULL REFERENCES users(id),
		private INTEGER NOT NULL DEFAULT 0,
		archived INTEGER NOT NULL DEFAULT 0,
		notes TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE categories (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE COLLATE NOCASE,
		kind TEXT NOT NULL DEFAULT 'expense',
		budget_cents INTEGER NOT NULL DEFAULT 0,
		color TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE transactions (
		id INTEGER PRIMARY KEY,
		date TEXT NOT NULL,
		amount_cents INTEGER NOT NULL,
		account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
		category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
		payee TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT '',
		user_id INTEGER NOT NULL REFERENCES users(id),
		private INTEGER NOT NULL DEFAULT 0,
		bill_id INTEGER,
		created TEXT NOT NULL
	);
	CREATE INDEX tx_date ON transactions(date);
	CREATE TABLE bills (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		amount_cents INTEGER NOT NULL DEFAULT 0,
		category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
		account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
		start_date TEXT NOT NULL,
		freq TEXT NOT NULL DEFAULT 'monthly',
		interval INTEGER NOT NULL DEFAULT 1,
		until TEXT NOT NULL DEFAULT '',
		autopay INTEGER NOT NULL DEFAULT 0,
		remind_days TEXT NOT NULL DEFAULT '3,0',
		assigned_to INTEGER REFERENCES users(id) ON DELETE SET NULL,
		owner_id INTEGER NOT NULL REFERENCES users(id),
		private INTEGER NOT NULL DEFAULT 0,
		url TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1
	);
	CREATE TABLE bill_payments (
		id INTEGER PRIMARY KEY,
		bill_id INTEGER NOT NULL REFERENCES bills(id) ON DELETE CASCADE,
		occ_date TEXT NOT NULL,
		paid_date TEXT NOT NULL,
		amount_cents INTEGER NOT NULL,
		user_id INTEGER NOT NULL REFERENCES users(id),
		transaction_id INTEGER,
		UNIQUE(bill_id, occ_date)
	);
	CREATE TABLE events (
		id INTEGER PRIMARY KEY,
		title TEXT NOT NULL,
		start_at TEXT NOT NULL,
		end_at TEXT NOT NULL DEFAULT '',
		all_day INTEGER NOT NULL DEFAULT 0,
		location TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT 'appointment',
		freq TEXT NOT NULL DEFAULT 'none',
		interval INTEGER NOT NULL DEFAULT 1,
		until TEXT NOT NULL DEFAULT '',
		remind_minutes TEXT NOT NULL DEFAULT '60',
		members TEXT NOT NULL DEFAULT '',
		owner_id INTEGER NOT NULL REFERENCES users(id),
		private INTEGER NOT NULL DEFAULT 0,
		color TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE notifications (
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		title TEXT NOT NULL,
		body TEXT NOT NULL DEFAULT '',
		link TEXT NOT NULL DEFAULT '',
		created TEXT NOT NULL,
		read INTEGER NOT NULL DEFAULT 0,
		delivery TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX notif_user ON notifications(user_id, id);
	CREATE TABLE sent_log (
		kind TEXT NOT NULL,
		item_id INTEGER NOT NULL,
		occ TEXT NOT NULL,
		lead TEXT NOT NULL,
		user_id INTEGER NOT NULL,
		sent TEXT NOT NULL,
		PRIMARY KEY(kind, item_id, occ, lead, user_id)
	);`,
	// 2: statement import and email reader
	`ALTER TABLE transactions ADD COLUMN external_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE transactions ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';
	ALTER TABLE transactions ADD COLUMN transfer INTEGER NOT NULL DEFAULT 0;
	CREATE UNIQUE INDEX tx_ext ON transactions(account_id, external_id) WHERE external_id <> '';
	CREATE INDEX tx_acc_date ON transactions(account_id, date);
	CREATE TABLE import_profiles (
		account_id INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
		mapping TEXT NOT NULL
	);
	CREATE TABLE mailboxes (
		id INTEGER PRIMARY KEY,
		label TEXT NOT NULL,
		host TEXT NOT NULL,
		port INTEGER NOT NULL DEFAULT 993,
		username TEXT NOT NULL,
		secret TEXT NOT NULL DEFAULT '',
		auth_type TEXT NOT NULL DEFAULT 'password',
		folder TEXT NOT NULL DEFAULT 'INBOX',
		enabled INTEGER NOT NULL DEFAULT 1,
		preview INTEGER NOT NULL DEFAULT 1,
		since_days INTEGER NOT NULL DEFAULT 60,
		uidvalidity INTEGER NOT NULL DEFAULT 0,
		last_uid INTEGER NOT NULL DEFAULT 0,
		last_check TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE email_rules (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		from_contains TEXT NOT NULL DEFAULT '',
		to_contains TEXT NOT NULL DEFAULT '',
		subject_contains TEXT NOT NULL DEFAULT '',
		body_contains TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT 'transaction',
		account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
		category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
		bill_id INTEGER REFERENCES bills(id) ON DELETE SET NULL,
		direction TEXT NOT NULL DEFAULT 'auto',
		priority INTEGER NOT NULL DEFAULT 100,
		enabled INTEGER NOT NULL DEFAULT 1
	);
	CREATE TABLE email_items (
		id INTEGER PRIMARY KEY,
		mailbox_id INTEGER,
		message_id TEXT NOT NULL UNIQUE,
		received TEXT NOT NULL,
		sender TEXT NOT NULL DEFAULT '',
		recipient TEXT NOT NULL DEFAULT '',
		subject TEXT NOT NULL DEFAULT '',
		rule_id INTEGER,
		kind TEXT NOT NULL DEFAULT '',
		amount_cents INTEGER NOT NULL DEFAULT 0,
		payee TEXT NOT NULL DEFAULT '',
		ref TEXT NOT NULL DEFAULT '',
		due_date TEXT NOT NULL DEFAULT '',
		account_id INTEGER,
		bill_id INTEGER,
		status TEXT NOT NULL,
		transaction_id INTEGER,
		note TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX email_items_status ON email_items(status, received);`,
	// 3: several folders per mailbox, watch folders for statement files
	`CREATE TABLE mailbox_folders (
		mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
		folder TEXT NOT NULL,
		uidvalidity INTEGER NOT NULL DEFAULT 0,
		last_uid INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(mailbox_id, folder)
	);
	INSERT INTO mailbox_folders(mailbox_id, folder, uidvalidity, last_uid) SELECT id, folder, uidvalidity, last_uid FROM mailboxes;
	CREATE TABLE watch_folders (
		id INTEGER PRIMARY KEY,
		label TEXT NOT NULL,
		path TEXT NOT NULL,
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		enabled INTEGER NOT NULL DEFAULT 1,
		last_scan TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE watched_files (
		watch_id INTEGER NOT NULL REFERENCES watch_folders(id) ON DELETE CASCADE,
		sha256 TEXT NOT NULL,
		name TEXT NOT NULL,
		seen TEXT NOT NULL,
		result TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(watch_id, sha256)
	);`,
	// 4: recurring income, low-balance alerts, subscription choices, one-tap action tokens
	`CREATE TABLE incomes (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		amount_cents INTEGER NOT NULL,
		account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
		start_date TEXT NOT NULL,
		freq TEXT NOT NULL DEFAULT 'monthly',
		interval INTEGER NOT NULL DEFAULT 1,
		until TEXT NOT NULL DEFAULT '',
		owner_id INTEGER NOT NULL REFERENCES users(id),
		private INTEGER NOT NULL DEFAULT 0,
		active INTEGER NOT NULL DEFAULT 1
	);
	ALTER TABLE accounts ADD COLUMN low_balance_cents INTEGER;
	CREATE TABLE subscription_state (
		key TEXT PRIMARY KEY,
		status TEXT NOT NULL,
		bill_id INTEGER
	);
	CREATE TABLE action_tokens (
		token_hash TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		bill_id INTEGER NOT NULL,
		occ TEXT NOT NULL,
		user_id INTEGER NOT NULL,
		expires TEXT NOT NULL,
		used TEXT NOT NULL DEFAULT ''
	);`,
	// 5: business vs personal, tax lines, category rules, split transactions, attachments
	`ALTER TABLE transactions ADD COLUMN business INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE transactions ADD COLUMN parent_id INTEGER REFERENCES transactions(id) ON DELETE CASCADE;
	ALTER TABLE transactions ADD COLUMN split INTEGER NOT NULL DEFAULT 0;
	CREATE INDEX tx_parent ON transactions(parent_id);
	ALTER TABLE accounts ADD COLUMN business INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE categories ADD COLUMN tax_line TEXT NOT NULL DEFAULT '';
	CREATE TABLE category_rules (
		id INTEGER PRIMARY KEY,
		contains TEXT NOT NULL,
		category_id INTEGER REFERENCES categories(id) ON DELETE CASCADE,
		business INTEGER NOT NULL DEFAULT -1,
		transfer INTEGER NOT NULL DEFAULT -1,
		priority INTEGER NOT NULL DEFAULT 100
	);
	CREATE TABLE attachments (
		id INTEGER PRIMARY KEY,
		kind TEXT NOT NULL,
		owner_id INTEGER NOT NULL REFERENCES users(id),
		transaction_id INTEGER REFERENCES transactions(id) ON DELETE CASCADE,
		document_id INTEGER,
		name TEXT NOT NULL,
		mime TEXT NOT NULL,
		size INTEGER NOT NULL,
		path TEXT NOT NULL,
		created TEXT NOT NULL
	);
	INSERT OR IGNORE INTO categories(name,kind,tax_line) VALUES
		('Business: Sales','income','1 Gross receipts or sales'),
		('Business: Advertising','expense','8 Advertising'),
		('Business: Fees & commissions','expense','10 Commissions and fees'),
		('Business: Contract labor','expense','11 Contract labor'),
		('Business: Office & software','expense','18 Office expense'),
		('Business: Supplies','expense','22 Supplies'),
		('Business: Shipping & postage','expense','27a Other expenses'),
		('Business: Bank & payment fees','expense','27a Other expenses'),
		('Business: Cost of goods','expense','Part III Cost of goods sold');`,
	// 6: savings goals, debt details
	`CREATE TABLE goals (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		target_cents INTEGER NOT NULL,
		target_date TEXT NOT NULL DEFAULT '',
		account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
		saved_cents INTEGER NOT NULL DEFAULT 0,
		owner_id INTEGER NOT NULL REFERENCES users(id),
		private INTEGER NOT NULL DEFAULT 0,
		color TEXT NOT NULL DEFAULT '',
		done INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE goal_moves (
		id INTEGER PRIMARY KEY,
		goal_id INTEGER NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
		date TEXT NOT NULL,
		cents INTEGER NOT NULL,
		note TEXT NOT NULL DEFAULT '',
		user_id INTEGER NOT NULL
	);
	ALTER TABLE accounts ADD COLUMN apr_bps INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE accounts ADD COLUMN min_payment_cents INTEGER NOT NULL DEFAULT 0;`,
	// 7: shared lists, chores, allowance, meal plan
	`CREATE TABLE lists (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		kind TEXT NOT NULL DEFAULT 'shopping',
		owner_id INTEGER NOT NULL REFERENCES users(id),
		private INTEGER NOT NULL DEFAULT 0,
		created TEXT NOT NULL
	);
	CREATE TABLE list_items (
		id INTEGER PRIMARY KEY,
		list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
		text TEXT NOT NULL,
		qty TEXT NOT NULL DEFAULT '',
		done INTEGER NOT NULL DEFAULT 0,
		done_by INTEGER,
		done_at TEXT NOT NULL DEFAULT '',
		assigned_to INTEGER,
		due_date TEXT NOT NULL DEFAULT '',
		position INTEGER NOT NULL DEFAULT 0,
		created_by INTEGER NOT NULL,
		created TEXT NOT NULL
	);
	CREATE TABLE chores (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		assigned_to INTEGER REFERENCES users(id) ON DELETE SET NULL,
		freq TEXT NOT NULL DEFAULT 'weekly',
		interval INTEGER NOT NULL DEFAULT 1,
		start_date TEXT NOT NULL,
		reward_cents INTEGER NOT NULL DEFAULT 0,
		active INTEGER NOT NULL DEFAULT 1,
		created_by INTEGER NOT NULL
	);
	CREATE TABLE chore_done (
		id INTEGER PRIMARY KEY,
		chore_id INTEGER NOT NULL REFERENCES chores(id) ON DELETE CASCADE,
		occ TEXT NOT NULL,
		user_id INTEGER NOT NULL,
		done_at TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'waiting',
		decided_by INTEGER,
		UNIQUE(chore_id, occ)
	);
	CREATE TABLE allowance (
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		date TEXT NOT NULL,
		cents INTEGER NOT NULL,
		note TEXT NOT NULL DEFAULT '',
		chore_done_id INTEGER,
		by_user INTEGER NOT NULL
	);
	CREATE TABLE meals (
		id INTEGER PRIMARY KEY,
		date TEXT NOT NULL,
		slot TEXT NOT NULL DEFAULT 'dinner',
		title TEXT NOT NULL,
		ingredients TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT '',
		created_by INTEGER NOT NULL
	);
	CREATE INDEX meals_date ON meals(date);`,
	// 8: two-step sign-in, change log
	`ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
	ALTER TABLE users ADD COLUMN totp_on INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE users ADD COLUMN recovery TEXT NOT NULL DEFAULT '';
	CREATE TABLE audit_log (
		id INTEGER PRIMARY KEY,
		at TEXT NOT NULL,
		user_id INTEGER,
		action TEXT NOT NULL,
		item TEXT NOT NULL DEFAULT '',
		ip TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX audit_at ON audit_log(at);`,
	// 9: calendar subscriptions, document vault, wall display
	`CREATE TABLE calendar_feeds (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		url TEXT NOT NULL,
		color TEXT NOT NULL DEFAULT '#8a63d2',
		members TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		last_fetch TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT '',
		skipped INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE feed_items (
		id INTEGER PRIMARY KEY,
		feed_id INTEGER NOT NULL REFERENCES calendar_feeds(id) ON DELETE CASCADE,
		uid TEXT NOT NULL,
		start_at TEXT NOT NULL,
		end_at TEXT NOT NULL DEFAULT '',
		all_day INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL,
		location TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX feed_items_start ON feed_items(start_at);
	CREATE TABLE documents (
		id INTEGER PRIMARY KEY,
		title TEXT NOT NULL,
		kind TEXT NOT NULL DEFAULT 'other',
		expires TEXT NOT NULL DEFAULT '',
		remind_days TEXT NOT NULL DEFAULT '30,7',
		notes TEXT NOT NULL DEFAULT '',
		owner_id INTEGER NOT NULL REFERENCES users(id),
		private INTEGER NOT NULL DEFAULT 1,
		created TEXT NOT NULL
	);
	CREATE TABLE display_tokens (
		token_hash TEXT PRIMARY KEY,
		label TEXT NOT NULL,
		created TEXT NOT NULL,
		last_seen TEXT NOT NULL DEFAULT ''
	);`,
	// 10: quiet hours, daily summary, recently deleted
	`ALTER TABLE users ADD COLUMN quiet_start INTEGER NOT NULL DEFAULT -1;
	ALTER TABLE users ADD COLUMN quiet_end INTEGER NOT NULL DEFAULT -1;
	ALTER TABLE users ADD COLUMN digest_hour INTEGER NOT NULL DEFAULT -1;
	ALTER TABLE notifications ADD COLUMN hold_until TEXT NOT NULL DEFAULT '';
	CREATE TABLE pending_sends (
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		notification_id INTEGER,
		message TEXT NOT NULL,
		due TEXT NOT NULL
	);
	CREATE TABLE trash (
		id INTEGER PRIMARY KEY,
		kind TEXT NOT NULL,
		label TEXT NOT NULL,
		owner_id INTEGER,
		private INTEGER NOT NULL DEFAULT 0,
		deleted_by INTEGER NOT NULL,
		deleted_at TEXT NOT NULL,
		data TEXT NOT NULL
	);`,
	// 11: mileage log, estimated tax category
	`CREATE TABLE trips (
		id INTEGER PRIMARY KEY,
		date TEXT NOT NULL,
		from_place TEXT NOT NULL DEFAULT '',
		to_place TEXT NOT NULL DEFAULT '',
		purpose TEXT NOT NULL,
		miles_x10 INTEGER NOT NULL,
		vehicle TEXT NOT NULL DEFAULT '',
		user_id INTEGER NOT NULL REFERENCES users(id),
		created TEXT NOT NULL
	);
	CREATE INDEX trips_date ON trips(date);
	INSERT OR IGNORE INTO categories(name,kind,tax_line) VALUES ('Estimated tax payments','expense','');`,
	// 12: shared costs between members, rollover budgets, yearly costs, statement checks
	`CREATE TABLE owes (
		id INTEGER PRIMARY KEY,
		date TEXT NOT NULL,
		description TEXT NOT NULL,
		amount_cents INTEGER NOT NULL,
		paid_by INTEGER NOT NULL REFERENCES users(id),
		kind TEXT NOT NULL DEFAULT 'expense',
		created_by INTEGER NOT NULL,
		created TEXT NOT NULL
	);
	CREATE TABLE owes_shares (
		owes_id INTEGER NOT NULL REFERENCES owes(id) ON DELETE CASCADE,
		user_id INTEGER NOT NULL REFERENCES users(id),
		share_cents INTEGER NOT NULL,
		PRIMARY KEY(owes_id, user_id)
	);
	ALTER TABLE categories ADD COLUMN rollover INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE categories ADD COLUMN rollover_from TEXT NOT NULL DEFAULT '';
	CREATE TABLE annual_costs (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		amount_cents INTEGER NOT NULL,
		month INTEGER NOT NULL,
		category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
		owner_id INTEGER NOT NULL REFERENCES users(id),
		private INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE reconciliations (
		id INTEGER PRIMARY KEY,
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		statement_date TEXT NOT NULL,
		statement_cents INTEGER NOT NULL,
		app_cents INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		created TEXT NOT NULL
	);`,
}

// Open opens (and creates or migrates) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer; keeps WAL simple and avoids SQLITE_BUSY
	s := &Store{DB: db, Path: path, Now: time.Now}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	var v int
	if err := s.DB.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if v == 0 {
		return s.seedCategories()
	}
	return nil
}

func (s *Store) seedCategories() error {
	exp := []string{"Housing", "Utilities", "Groceries", "Dining", "Transportation", "Insurance", "Health", "Kids", "Pets", "Subscriptions", "Entertainment", "Clothing", "Gifts", "Debt", "Savings", "Other"}
	inc := []string{"Paycheck", "Benefits", "Other income"}
	for _, n := range exp {
		if _, err := s.DB.Exec(`INSERT OR IGNORE INTO categories(name,kind) VALUES(?, 'expense')`, n); err != nil {
			return err
		}
	}
	for _, n := range inc {
		if _, err := s.DB.Exec(`INSERT OR IGNORE INTO categories(name,kind) VALUES(?, 'income')`, n); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// Backup writes a consistent copy of the database to dst.
func (s *Store) Backup(dst string) error {
	os.Remove(dst)
	_, err := s.DB.Exec(`VACUUM INTO ?`, dst)
	return err
}

func (s *Store) nowStr() string { return s.Now().Format(time.RFC3339) }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
