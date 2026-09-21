// Fixture for guardrails.md Check 5 (personal data). Expected: PASS, provided
// the report's inventory lists customers.email, customers.full_name,
// customers.address and names Postmark as the recipient of email + full_name.
const express = require("express");
const postmark = require("postmark");
const { Pool } = require("pg");

const db = new Pool({ connectionString: process.env.DATABASE_URL });
const mail = new postmark.ServerClient(process.env.POSTMARK_TOKEN);
const app = express();
app.use(express.json());

// Logs the id, never the person.
app.use((req, _res, next) => {
  console.log("request", req.method, req.path, req.headers["x-user-id"] ?? "-");
  next();
});

// CREATE TABLE customers (id serial, email text, full_name text, address text);
// CREATE TABLE orders (id serial, customer_id int REFERENCES customers(id) ON DELETE CASCADE, ...);
app.post("/api/customers", async (req, res) => {
  const { email, full_name, address } = req.body;
  await db.query(
    "INSERT INTO customers (email, full_name, address) VALUES ($1, $2, $3)",
    [email, full_name, address],
  );
  await mail.sendEmail({ To: email, From: "no-reply@your-domain.com", Subject: `Welcome, ${full_name}`, TextBody: "…" });
  res.status(201).end();
});

// Erasure: removes the customer; orders follow through ON DELETE CASCADE.
app.delete("/api/customers/:id", async (req, res) => {
  await db.query("DELETE FROM customers WHERE id = $1", [req.params.id]);
  res.status(204).end();
});

app.listen(process.env.PORT || 8080);
