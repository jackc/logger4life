# Logger4Life

Logger4Life is tool to quickly log events. For example:

* Taking vitamins
* Counting pushups
* Changing diapers
* Standing up and stretching

You can define custom event types. Each event type will have a button to quickly create an entry. Event types can define additional attributes, such as a quantity. For example, "pushups" may have the number of pushups performed.

## Technical Design

### Persistence

* Data is stored in PostgreSQL.
* tern is used for migration management.
* UUIDs preferred for primary keys. Use v7 unless creation time should not be exposed, then use v4.

### Backend Server

* Backend written in Go.
* Use either the builtin Go router or Chi.
* JSON API's provided for SPA frontend.
* Use pgx for database driver
* Use cobra for CLI parsing

### Frontend SPA

* Use Sveltekit

### Testing

* The backend server should be tested with testify.
* Browser integration tests should be performed with Playwright.
