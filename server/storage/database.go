package storage

import (
	"database/sql"

	"github.com/go-sql-driver/mysql"
)

// MySQLStorage implements a versioned store using a relational database.
// The database table must look like:
// CREATE TABLE `tuf_files` (
//   `id` INT AUTO_INCREMENT,
//   `gun` VARCHAR(255) NOT NULL
//   `role` VARCHAR(255) NOT NULL
//   `version` INT
//   `data` LONGBLOB
//   PRIMARY KEY (`id`)
//   UNIQUE INDEX (`gun`, `role`, `version`)
// ) DEFAULT CHARSET=utf8;
//
// CREATE TABLE `timestamp_keys` (
//   `gun` VARCHAR(255),
//   `cipher` VARCHAR(30),
//   `public` BLOB NOT NULL,
// ) DEFAULT CHARSET=utf8;
type MySQLStorage struct {
	sql.DB
}

func NewMySQLStorage(db *sql.DB) *MySQLStorage {
	return &MySQLStorage{
		DB: *db,
	}
}

// Update multiple TUF records in a single transaction.
// Always insert a new row. The unique constraint will ensure there is only ever
func (db *MySQLStorage) UpdateCurrent(gun, role string, version int, data []byte) error {
	checkStmt := "SELECT count(*) FROM `tuf_files` WHERE `gun`=? AND `role`=? AND `version`>=?;"
	insertStmt := "INSERT INTO `tuf_files` (`gun`, `role`, `version`, `data`) VALUES (?,?,?,?) ;"

	// ensure immediately previous version exists
	row := db.QueryRow(checkStmt, gun, role, version)
	var exists int
	err := row.Scan(&exists)
	if err != nil {
		return err
	}
	if exists != 0 {
		return &ErrOldVersion{}
	}

	// attempt to insert. Due to race conditions with the check this could fail.
	// That's OK, we're doing first write wins. The client will be messaged it
	// needs to rebase.
	_, err = db.Exec(insertStmt, gun, role, version, data)
	if err != nil {
		// need to check error type for duplicate key exception
		// and return ErrOldVersion if duplicate
		return err
	}
	return nil
}

// Get a specific TUF record
func (db *MySQLStorage) GetCurrent(gun, tufRole string) (data []byte, err error) {
	stmt := "SELECT `data` FROM `tuf_files` WHERE `gun`=? AND `role`=? ORDER BY `version` DESC LIMIT 1;"
	rows, err := db.Query(stmt, gun, tufRole) // this should be a QueryRow()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// unique constraint on (gun, role) will ensure only one row is returned (or none if no match is found)
	if !rows.Next() {
		return nil, &ErrNotFound{}
	}

	err = rows.Scan(&data)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (db *MySQLStorage) Delete(gun string) error {
	stmt := "DELETE FROM `tuf_files` WHERE `gun`=?;"
	_, err := db.Exec(stmt, gun)
	return err
}
func (db *MySQLStorage) GetTimestampKey(gun string) (cipher string, public []byte, err error) {
	stmt := "SELECT `cipher`, `public` FROM `timestamp_keys` WHERE `gun`=?;"
	row := db.QueryRow(stmt, gun)

	err = row.Scan(&cipher, &public)
	if err == sql.ErrNoRows {
		return "", nil, ErrNoKey{gun: gun}
	} else if err != nil {
		return "", nil, err
	}

	return cipher, public, err
}
func (db *MySQLStorage) SetTimestampKey(gun, cipher string, public []byte) error {
	stmt := "INSERT INTO `timestamp_keys` (`gun`, `cipher`, `public`) VALUES (?,?,?);"
	_, err := db.Exec(stmt, gun, cipher, public)
	if err, ok := err.(*mysql.MySQLError); ok {
		if err.Number == 1022 { // duplicate key error
			return &ErrTimestampKeyExists{gun: gun}
		}
	} else if err != nil {
		return err
	}
	return nil
}
