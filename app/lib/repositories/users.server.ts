import { type Db, newId, nowIso } from "../db.server";

export interface UserRecord {
  id: string;
  email: string;
  password_hash: string;
  created_at: string;
  updated_at: string;
}

export async function createUser(
  db: Db,
  email: string,
  passwordHash: string,
): Promise<UserRecord> {
  const now = nowIso();
  const user: UserRecord = {
    id: newId("usr"),
    email,
    password_hash: passwordHash,
    created_at: now,
    updated_at: now,
  };

  await db
    .prepare(
      `INSERT INTO users (id, email, password_hash, created_at, updated_at)
       VALUES (?, ?, ?, ?, ?)`,
    )
    .bind(user.id, user.email, user.password_hash, user.created_at, user.updated_at)
    .run();

  return user;
}

export async function findUserByEmail(db: Db, email: string): Promise<UserRecord | null> {
  return await db
    .prepare(
      `SELECT id, email, password_hash, created_at, updated_at
       FROM users
       WHERE email = ?
       LIMIT 1`,
    )
    .bind(email)
    .first<UserRecord>();
}

export async function findUserById(db: Db, id: string): Promise<UserRecord | null> {
  return await db
    .prepare(
      `SELECT id, email, password_hash, created_at, updated_at
       FROM users
       WHERE id = ?
       LIMIT 1`,
    )
    .bind(id)
    .first<UserRecord>();
}
