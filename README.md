# Osemu Finance - A currency rates tracker

Osemu Finance is a currency rates tracker that allows users to track the exchange rates of different currencies. The app was built in Go and pulls data directly from the Stanbic rates according to the market.

## How it works

The exchange rates are updated every day at 10:00AM by pulling the updated pdf from the Stanbic website. The pdf is then parsed and the data is stored in a database. The app then exposes an API that is used to expose the data to the frontend.

## Tech

- Go
- SQLite
