// Fixture for guardrails.md Check 5 (personal data). Expected: BLOCKER.
// A minimal Express app that stores personal data and has no way to delete a
// person, logs the whole request body, and ships it to an error tracker.
const express = require("express");
const Sentry = require("@sentry/node");
const { Pool } = require("pg");

const db = new Pool({ connectionString: process.env.DATABASE_URL });
const app = express();
app.use(express.json());

// Every request body — including email, name and address — goes to stdout.
app.use((req, _res, next) => {
  console.log("request", req.method, req.path, req.body);
  next();
});

// CREATE TABLE customers (id serial, email text, full_name text, address text, ip text);
app.post("/api/customers", async (req, res) => {
  const { email, full_name, address } = req.body;
  await db.query(
    "INSERT INTO customers (email, full_name, address, ip) VALUES ($1, $2, $3, $4)",
    [email, full_name, address, req.ip],
  );
  Sentry.setUser({ email, username: full_name });
  res.status(201).end();
});

app.get("/api/customers/:id", async (req, res) => {
  const { rows } = await db.query("SELECT * FROM customers WHERE id = $1", [req.params.id]);
  res.json(rows[0] ?? null);
});

// No DELETE route, no admin action, no documented procedure removes a customer.

app.listen(process.env.PORT || 8080);
