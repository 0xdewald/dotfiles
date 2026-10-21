// Package names generates "Adjective Noun" agent names.
package names

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

var adjectives = strings.Fields(`
Amber Brave Breezy Bright Bubbly Calm Cheeky Chilly Clever Cosmic
Cozy Crispy Curious Dapper Dizzy Dusty Eager Electric Fancy Fearless
Fizzy Fluffy Frosty Fuzzy Gentle Giddy Glossy Golden Grumpy Happy
Hasty Humble Jazzy Jolly Jumpy Lazy Lucky Mellow Merry Mighty
Misty Nimble Noble Peppy Plucky Quiet Quirky Rapid Rusty Salty
Sleepy Snappy Sneaky Spicy Sunny Swift Tidy Toasty Witty Zesty`)

var nouns = strings.Fields(`
Badgers Bagels Beacons Biscuits Bobcats Buttons Cactus Comets Cookies Coyotes
Crickets Cupcakes Dingos Dumplings Falcons Ferrets Fireflies Foxes Gadgets Geckos
Gophers Hedgehogs Herons Koalas Lemurs Llamas Lobsters Lynxes Magpies Mangoes
Marmots Meerkats Muffins Narwhals Noodles Octopi Otters Owls Pancakes Pandas
Parrots Pebbles Pickles Penguins Pixels Pretzels Puffins Quokkas Raccoons Ravens
Rockets Sparrows Sprockets Squids Tacos Toucans Turnips Waffles Walruses Wombats`)

// New returns a random name not rejected by taken. After many collisions it
// appends a number, so it always terminates.
func New(taken func(string) bool) string {
	for i := 0; ; i++ {
		n := adjectives[rand.IntN(len(adjectives))] + " " + nouns[rand.IntN(len(nouns))]
		if i >= 200 {
			n = fmt.Sprintf("%s %d", n, i)
		}
		if taken == nil || !taken(n) {
			return n
		}
	}
}

// Slug turns "Fuzzy Muffins" into "fuzzy-muffins".
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}
