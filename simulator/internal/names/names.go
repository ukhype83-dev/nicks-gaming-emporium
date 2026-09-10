// Package names provides locale-aware synthetic first/last name
// generation for customer and staff records.
//
// V1 skeleton uses small editorial dictionaries embedded as Go data.
// Each bank is ~15–30 first names + ~15–30 surnames per country.
// Not perfect statistical realism; perfect enough to recognise which
// country a customer is from in a test query, which is what matters
// pedagogically.
//
// V1.5 replaces these with real public-domain sources:
//   - US SSA baby names by year (public domain)
//   - US Census surnames with frequencies (public domain)
//   - UK ONS baby names (OGL)
//   - Wikipedia "most common surnames by country" lists (CC BY-SA)
// Upgrade paths are tracked in LICENSE.md §4.
package names

import (
	"math/rand/v2"
)

// NameBank holds first/last names for one locale.
type NameBank struct {
	First []string
	Last  []string
}

// Banks indexed by ISO 3166-1 alpha-2 country code. Unknown codes fall
// back to US via Pick.
var Banks = map[string]NameBank{
	"US": {
		First: []string{"James", "John", "Robert", "Michael", "William", "David", "Richard", "Joseph",
			"Thomas", "Charles", "Christopher", "Daniel", "Matthew", "Anthony", "Donald", "Mark",
			"Mary", "Patricia", "Jennifer", "Linda", "Elizabeth", "Barbara", "Susan", "Jessica",
			"Sarah", "Karen", "Nancy", "Lisa", "Emily", "Ashley", "Olivia"},
		Last: []string{"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis",
			"Rodriguez", "Martinez", "Hernandez", "Lopez", "Gonzalez", "Wilson", "Anderson",
			"Thomas", "Taylor", "Moore", "Jackson", "Martin", "Lee", "Perez", "Thompson", "White"},
	},
	"GB": {
		First: []string{"Oliver", "George", "Harry", "Jack", "Charlie", "Alfie", "Jacob", "Thomas",
			"William", "James", "Oscar", "Leo", "Freddie", "Noah", "Ethan", "Harvey",
			"Emily", "Olivia", "Isla", "Amelia", "Sophia", "Ava", "Lily", "Grace",
			"Evie", "Poppy", "Ella", "Willow", "Freya", "Charlotte", "Sophie"},
		Last: []string{"Smith", "Jones", "Taylor", "Brown", "Williams", "Wilson", "Johnson", "Davies",
			"Robinson", "Wright", "Thompson", "Evans", "Walker", "White", "Roberts", "Green",
			"Hall", "Wood", "Jackson", "Clark", "Patel", "Khan", "Clarke", "Lewis"},
	},
	"AU": {
		First: []string{"Oliver", "Jack", "William", "Noah", "Henry", "Lucas", "Charlie", "Ethan",
			"James", "Thomas", "Leo", "Liam", "Mason", "Benjamin", "Alexander",
			"Charlotte", "Olivia", "Amelia", "Mia", "Isla", "Ava", "Grace", "Chloe",
			"Emily", "Sophie", "Zoe", "Harper", "Evie"},
		Last: []string{"Smith", "Jones", "Williams", "Brown", "Wilson", "Taylor", "Johnson", "White",
			"Martin", "Anderson", "Thompson", "Nguyen", "Ryan", "Harris", "Lee",
			"Walker", "King", "Hall", "Baker", "Campbell"},
	},
	"CA": {
		First: []string{"Liam", "Noah", "Oliver", "William", "Benjamin", "Lucas", "Jackson", "Ethan",
			"Jacob", "Mason", "Logan", "Gabriel", "Nathan", "Samuel", "Alexandre",
			"Olivia", "Emma", "Charlotte", "Sophia", "Ava", "Chloe", "Mia", "Léa",
			"Emily", "Amelia", "Zoe", "Florence", "Alice"},
		Last: []string{"Smith", "Brown", "Tremblay", "Martin", "Roy", "Gagnon", "Lee", "Wilson",
			"Johnson", "MacDonald", "Taylor", "Campbell", "Anderson", "White", "Thompson",
			"Young", "Mitchell", "Cote", "Chen", "Singh"},
	},
	"DE": {
		First: []string{"Michael", "Stefan", "Andreas", "Thomas", "Klaus", "Jürgen", "Christian",
			"Markus", "Martin", "Sebastian", "Alexander", "Daniel", "Tim", "Lukas", "Max",
			"Maria", "Anna", "Petra", "Sabine", "Martina", "Julia", "Sandra", "Claudia",
			"Andrea", "Birgit", "Lea", "Hannah", "Lena", "Sophie", "Laura"},
		Last: []string{"Müller", "Schmidt", "Schneider", "Fischer", "Weber", "Meyer", "Wagner",
			"Becker", "Schulz", "Hoffmann", "Schäfer", "Koch", "Bauer", "Richter", "Klein",
			"Wolf", "Schröder", "Neumann", "Schwarz", "Zimmermann", "Braun", "Krüger"},
	},
	"FR": {
		First: []string{"Jean", "Pierre", "Michel", "Alain", "Philippe", "Bernard", "Christophe",
			"Laurent", "Nicolas", "Frédéric", "Olivier", "Sébastien", "Thomas", "Lucas",
			"Marie", "Monique", "Françoise", "Isabelle", "Catherine", "Sophie", "Martine",
			"Nathalie", "Sylvie", "Valérie", "Claire", "Emma", "Léa", "Chloé", "Camille"},
		Last: []string{"Martin", "Bernard", "Dubois", "Thomas", "Robert", "Richard", "Petit",
			"Durand", "Leroy", "Moreau", "Simon", "Laurent", "Lefebvre", "Michel", "Garcia",
			"David", "Bertrand", "Roux", "Vincent", "Fournier"},
	},
	"ES": {
		First: []string{"Antonio", "Manuel", "José", "Francisco", "Juan", "David", "Javier", "Daniel",
			"Carlos", "Miguel", "Jesús", "Rafael", "Alejandro", "Pedro",
			"María", "Carmen", "Ana", "Isabel", "Laura", "Cristina", "Dolores", "Pilar",
			"Teresa", "Rosa", "Lucía", "Sofía", "Paula"},
		Last: []string{"García", "Rodríguez", "González", "Fernández", "López", "Martínez", "Sánchez",
			"Pérez", "Gómez", "Martín", "Jiménez", "Ruiz", "Hernández", "Díaz", "Moreno",
			"Álvarez", "Muñoz", "Romero", "Alonso", "Gutiérrez"},
	},
	"IT": {
		First: []string{"Giuseppe", "Giovanni", "Antonio", "Francesco", "Luigi", "Mario", "Andrea",
			"Marco", "Paolo", "Alessandro", "Luca", "Matteo", "Pietro", "Stefano",
			"Maria", "Anna", "Rosa", "Francesca", "Giulia", "Chiara", "Laura", "Elena",
			"Paola", "Silvia", "Martina", "Sara"},
		Last: []string{"Rossi", "Russo", "Ferrari", "Esposito", "Bianchi", "Romano", "Colombo",
			"Ricci", "Marino", "Greco", "Bruno", "Gallo", "Conti", "De Luca", "Mancini",
			"Costa", "Giordano", "Rizzo", "Lombardi", "Moretti"},
	},
	"NL": {
		First: []string{"Jan", "Piet", "Klaas", "Willem", "Hendrik", "Johan", "Kees", "Bas", "Daan",
			"Sem", "Luuk", "Finn", "Lars", "Milan", "Thomas",
			"Maria", "Anna", "Johanna", "Elisabeth", "Cornelia", "Sophie", "Lotte", "Emma",
			"Julia", "Zoë", "Tess", "Eva", "Sara"},
		Last: []string{"De Jong", "Jansen", "De Vries", "Van den Berg", "Van Dijk", "Bakker",
			"Janssen", "Visser", "Smit", "Meijer", "De Boer", "Mulder", "De Groot",
			"Bos", "Vos", "Peters", "Hendriks", "Van Leeuwen", "Dekker", "Brouwer"},
	},
	"JP": {
		First: []string{"Hiroshi", "Takashi", "Kenji", "Taro", "Shin", "Akira", "Daisuke", "Ryota",
			"Yuto", "Haruto", "Sota", "Ren", "Yuki", "Kaito",
			"Sakura", "Aoi", "Hana", "Mio", "Nanami", "Haruka", "Ayumi", "Misaki", "Rina",
			"Yui", "Hinata", "Rin", "Mei", "Akari", "Himari"},
		Last: []string{"Sato", "Suzuki", "Takahashi", "Tanaka", "Watanabe", "Ito", "Yamamoto",
			"Nakamura", "Kobayashi", "Kato", "Yoshida", "Yamada", "Sasaki", "Yamaguchi",
			"Matsumoto", "Inoue", "Kimura", "Hayashi", "Shimizu", "Saito"},
	},
	"KR": {
		First: []string{"Min-jun", "Seo-jun", "Do-yun", "Ji-ho", "Eun-woo", "Joon-woo", "Yu-jun",
			"Ha-jun", "Si-woo", "Geon-woo",
			"Seo-yeon", "Ji-woo", "Seo-hyun", "Min-seo", "Ha-eun", "Yoon-seo", "Ji-a",
			"Ha-yoon", "Ye-rin", "Soo-bin"},
		Last: []string{"Kim", "Lee", "Park", "Choi", "Jung", "Kang", "Cho", "Yoon", "Jang", "Lim",
			"Han", "Oh", "Shin", "Seo", "Kwon", "Hwang", "Ahn", "Song", "Yoo", "Hong"},
	},
	"BR": {
		First: []string{"José", "João", "Antonio", "Francisco", "Carlos", "Paulo", "Pedro", "Lucas",
			"Marcos", "Rafael", "Daniel", "Felipe", "Gabriel", "Gustavo",
			"Maria", "Ana", "Francisca", "Antonia", "Adriana", "Juliana", "Mariana", "Fernanda",
			"Amanda", "Beatriz", "Camila", "Larissa", "Letícia"},
		Last: []string{"Silva", "Santos", "Oliveira", "Souza", "Rodrigues", "Ferreira", "Alves",
			"Pereira", "Lima", "Gomes", "Ribeiro", "Carvalho", "Almeida", "Lopes", "Soares",
			"Fernandes", "Vieira", "Barbosa", "Rocha", "Dias"},
	},
	"CH": {
		First: []string{"Hans", "Peter", "Urs", "Markus", "Daniel", "Thomas", "Stefan", "Martin",
			"Andreas", "Luca", "Noah", "Leon",
			"Maria", "Anna", "Ursula", "Verena", "Marianne", "Sophie", "Emma", "Mia"},
		Last: []string{"Meier", "Müller", "Schmid", "Keller", "Huber", "Weber", "Schneider", "Meyer",
			"Steiner", "Favre", "Bernasconi", "Rossi", "Lehmann", "Wyss", "Brunner"},
	},
	"SE": {
		First: []string{"Erik", "Lars", "Karl", "Anders", "Per", "Johan", "Nils", "Olof", "Gunnar",
			"Carl", "Oscar", "Axel", "William", "Hugo", "Liam",
			"Maria", "Anna", "Eva", "Kristina", "Margareta", "Elsa", "Alice", "Wilma",
			"Alma", "Klara"},
		Last: []string{"Andersson", "Johansson", "Karlsson", "Nilsson", "Eriksson", "Larsson",
			"Olsson", "Persson", "Svensson", "Gustafsson", "Pettersson", "Jonsson", "Jansson",
			"Hansson", "Bengtsson", "Jönsson", "Lindberg", "Jakobsson"},
	},
	"NO": {
		First: []string{"Ola", "Kari", "Jens", "Lars", "Hans", "Knut", "Per", "Ole", "Erik", "Nils",
			"Emil", "Filip", "Jakob", "Lucas", "Oscar",
			"Anna", "Marit", "Kari", "Ingrid", "Ingeborg", "Astrid", "Nora", "Emma",
			"Sofie", "Ella"},
		Last: []string{"Hansen", "Johansen", "Olsen", "Larsen", "Andersen", "Pedersen", "Nilsen",
			"Kristiansen", "Jensen", "Karlsen", "Johnsen", "Pettersen", "Eriksen", "Berg",
			"Haugen", "Hagen"},
	},
	"DK": {
		First: []string{"Jens", "Peter", "Michael", "Lars", "Henrik", "Søren", "Kim", "Hans",
			"Anders", "Thomas", "Niels", "Christian",
			"Mette", "Anna", "Kirsten", "Hanne", "Marianne", "Lone", "Susanne", "Pia",
			"Emma", "Ida", "Sofie"},
		Last: []string{"Jensen", "Nielsen", "Hansen", "Pedersen", "Andersen", "Christensen", "Larsen",
			"Sørensen", "Rasmussen", "Jørgensen", "Petersen", "Madsen", "Kristensen",
			"Olsen", "Thomsen", "Christiansen"},
	},
	"PL": {
		First: []string{"Jan", "Piotr", "Krzysztof", "Tomasz", "Andrzej", "Paweł", "Marcin", "Michał",
			"Jakub", "Adam", "Stanisław", "Mateusz", "Grzegorz", "Wojciech",
			"Anna", "Maria", "Katarzyna", "Małgorzata", "Agnieszka", "Barbara", "Ewa",
			"Krystyna", "Magdalena", "Joanna", "Zuzanna"},
		Last: []string{"Nowak", "Kowalski", "Wiśniewski", "Wójcik", "Kowalczyk", "Kamiński",
			"Lewandowski", "Zieliński", "Szymański", "Woźniak", "Dąbrowski", "Kozłowski",
			"Jankowski", "Mazur", "Kwiatkowski", "Krawczyk"},
	},
	"CZ": {
		First: []string{"Jan", "Petr", "Jiří", "Pavel", "Martin", "Tomáš", "Jaroslav", "Miroslav",
			"Josef", "František", "Karel", "Lukáš",
			"Jana", "Marie", "Eva", "Hana", "Anna", "Lenka", "Věra", "Kateřina",
			"Helena", "Tereza", "Alena"},
		Last: []string{"Novák", "Svoboda", "Novotný", "Dvořák", "Černý", "Procházka", "Kučera",
			"Veselý", "Horák", "Němec", "Pokorný", "Marek", "Růžička", "Beneš"},
	},
}

// Pick returns a deterministic (first, last) pair for the given
// country. Falls back to US names for unknown countries.
func Pick(country string, r *rand.Rand) (string, string) {
	bank, ok := Banks[country]
	if !ok || len(bank.First) == 0 || len(bank.Last) == 0 {
		bank = Banks["US"]
	}
	first := bank.First[r.IntN(len(bank.First))]
	last := bank.Last[r.IntN(len(bank.Last))]
	return first, last
}
