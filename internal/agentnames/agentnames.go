// Package agentnames provides Rick & Morty character names and colors for
// swarm/subagent identification in the TUI.
package agentnames

import (
	"math/rand"
	"sync"
	"time"
)

// AgentColor holds a name and its TUI color.
type AgentColor struct {
	Name  string
	Color string
}

var (
	mu    sync.Mutex
	rng   = rand.New(rand.NewSource(time.Now().UnixNano()))
	pool  []AgentColor
	inUse = map[string]struct{}{}
)

func init() {
	pool = []AgentColor{
		{"Rick", "#00D4AA"},
		{"Morty", "#FFE600"},
		{"Summer", "#FF6EC7"},
		{"Beth", "#E56B6B"},
		{"Jerry", "#00E5FF"},
		{"Mr. Meeseeks", "#FF9F43"},
		{"Squanchy", "#A29BFE"},
		{"Birdperson", "#54A0FF"},
		{"Unity", "#FF6B81"},
		{"Evil Morty", "#2C2C54"},
		{"Poombie", "#4CD137"},
		{"Krombopulos Michael", "#EE5A24"},
		{"Tiny Rick", "#C4E538"},
		{"Pickle Rick", "#009432"},
		{"President Morty", "#12CBC4"},
		{"Simple Rick", "#FDA7DF"},
		{"Hamurai", "#B53471"},
		{"Jessica", "#F79F1F"},
		{"Mr. Nimbus", "#5758BB"},
		{"Noob-Noob", "#EAB54C"},
		{"Gazorpazorpfield", "#C44569"},
		{"Snuffles", "#706FD3"},
		{"Scary Terry", "#2C3A47"},
		{"Phoenixperson", "#FF3838"},
		{"Planetina", "#2ED573"},
		{"Supernova", "#9AECDB"},
		{"Vindicators", "#E1DA00"},
		{"Zick Zack", "#5858FF"},
		{"Glexo", "#40E0D0"},
		{"Blim Blam", "#FF4757"},
		{"Eyeholes Man", "#5352ED"},
		{"Mr. Beauregard", "#A4B0BE"},
		{"Naruto Smith", "#E77F67"},
		{"Jacqueline", "#7BED9F"},
		{"Heist-o-Tron", "#70A1FF"},
		{"Rand-o-Tron", "#5352ED"},
		{"Hamster-in-Butt", "#DCDDE1"},
		{"Keara", "#E15F41"},
		{"Luci", "#4B6584"},
		{"Ants-in-Eyes", "#2C2C54"},
		{"Bobby", "#227093"},
		{"Tinkles", "#34ACE0"},
		{"Mr. Goldminty", "#33D9B2"},
		{"Balze", "#FF6348"},
		{"Glenn", "#1289A7"},
		{"Gazorpians", "#C4E538"},
		{"Antenna", "#FFDD59"},
		{"Shrimply", "#E77F67"},
		{"Korvo", "#34ACE0"},
		{"Terry", "#A4B0BE"},
		{"Slippery", "#7B8C86"},
		{"Assassin", "#2C3A47"},
		{"Mitch", "#EAB54C"},
		{"Walkie-Talkie", "#40407a"},
		{"Space Beth", "#1B9CFC"},
		{"Memory Rick", "#9AECDB"},
		{"Memory Morty", "#12CBC4"},
		{"Dissatisfied Jerry", "#576574"},
		{"Investigator Rick", "#485460"},
		{"Sarge", "#C44569"},
		{"Bart", "#B3B1AD"},
		{"Crowthor", "#2C2C54"},
		{"Old World", "#7B6F4E"},
		{"Plutonians", "#10A881"},
		{"Flansian", "#34ACE0"},
		{"Daleks", "#5758BB"},
		{"Zigerions", "#EE5A24"},
		{"Scroopy Noopers", "#1B9CFC"},
		{"Pibbles", "#FDA7DF"},
		{"CEO", "#E1DA00"},
		{"Roy", "#33D9B2"},
		{"Dale", "#FF6348"},
		{"Knights", "#576574"},
		{"Liquid manifestation", "#9AECDB"},
		{"Prince", "#FDA7DF"},
		{"Crocubot", "#10A881"},
		{"Genocider", "#C44569"},
		{"Yancy", "#EAB54C"},
		{"Zoey", "#2ED573"},
		{"Poncho", "#E77F67"},
		{"Gene", "#1289A7"},
		{"Bootleg Portal Chemist", "#5352ED"},
		{"Assassin Leader", "#2C3A47"},
		{"Paper", "#FDA7DF"},
		{"Roy's Wife", "#34ACE0"},
		{"Kyle", "#E15F41"},
		{"Unknown-67", "#4B6584"},
		{"Insurance God", "#B53471"},
		{"Insurance Goblin", "#2ED573"},
		{"Snake Queen", "#009432"},
		{"Snake Prime", "#34ACE0"},
		{"Flower", "#FDA7DF"},
		{"Giant Raptor", "#FF4757"},
		{"Ethan", "#A4B0BE"},
		{"Ethan's Friend", "#7B8C86"},
		{"Sir Silas", "#576574"},
		{"Open Door", "#E1DA00"},
		{"Gremlin", "#4CD137"},
		{"Black Hole Morty", "#2C2C54"},
		{"Time Bird", "#485460"},
		{"Idiosyncratic", "#4B6584"},
		{"Lava World", "#EE5A24"},
		{"Tree of Truth", "#009432"},
		{"Robot", "#576574"},
		{"Captain Captainson", "#E1DA00"},
		{"Captain Hize", "#1B9CFC"},
		{"Recruiter", "#A4B0BE"},
		{"Dance Mopping", "#FDA7DF"},
		{"Balthromaw", "#10A881"},
		{"Talking Cat", "#FF6348"},
		{"King Jellybean", "#FDA7DF"},
		{"Reuben", "#E1DA00"},
		{"Pencilvester", "#12CBC4"},
		{"Fart", "#2ED573"},
		{"Cynthia", "#FF6EC7"},
		{"Phoenix", "#FF3838"},
		{"Skath", "#485460"},
		{"Thrombey", "#B3B1AD"},
		{"Dippled", "#576574"},
		{"Zarh", "#706FD3"},
		{"Plixi", "#5352ED"},
		{"Lird", "#2C2C54"},
		{"Real Fake Doors", "#E1DA00"},
		{"Bloopy", "#34ACE0"},
		{"Johnny Rita", "#EE5A24"},
		{"Bloobshire", "#5758BB"},
		{"Kroot", "#C44569"},
		{"Jericho", "#FDA7DF"},
		{"Clone Beth", "#1B9CFC"},
		{"Spare Parts Morty", "#1289A7"},
		{"Lightspeed Bone", "#34ACE0"},
		{"Tiny Jedi", "#A4B0BE"},
		{"Helmet M-87", "#576574"},
		{"Risotto Groupon", "#FF4757"},
		{"Wishing Portal", "#5352ED"},
		{"Gork", "#7B8C86"},
		{"Cryo-Stasis Morty", "#12CBC4"},
		{"Scaffold", "#EAB54C"},
		{"Hamster-Lord", "#2C2C54"},
		{"Maxi", "#FF6348"},
		{"Vermigurber", "#E1DA00"},
		{"Ventriloquiver", "#34ACE0"},
		{"Exo-Alpha", "#4B6584"},
		{"Turbulent Skies", "#5758BB"},
		{"Scary Mr. Goldenfold", "#E1DA00"},
		{"Larry", "#A4B0BE"},
		{"Heistotron", "#5352ED"},
		{"Randotron", "#706FD3"},
		{"Charpi", "#FF9F43"},
		{"Melderman", "#FF6B81"},
		{"Hole in the Wall Morty", "#12CBC4"},
		{"Scareaway Morty", "#2C2C54"},
		{"Boss", "#C44569"},
		{"Turbo", "#FF4757"},
		{"Nagus", "#1B9CFC"},
		{"Barbecue", "#EE5A24"},
		{"Mr. Stop-Looking", "#EAB54C"},
		{"Mr. Sneaky", "#576574"},
		{"Paha", "#4B6584"},
		{"Taint Washer", "#706FD3"},
		{"Garblov", "#C4E538"},
		{"Old World Director", "#576574"},
		{"Lil B", "#34ACE0"},
		{"Kiki", "#FDA7DF"},
		{"Chachi", "#2ED573"},
		{"Chip", "#FF6348"},
		{"Birdperson's Wife", "#54A0FF"},
		{"Shono", "#12CBC4"},
		{"The Observer", "#485460"},
		{"Rick's Car", "#576574"},
		{"Risotto's Chef", "#FF4757"},
		{"Jerry's Therapist", "#12CBC4"},
		{"Mr. Needful", "#E1DA00"},
		{"Voltematron", "#5352ED"},
		{"Ethan's Crush", "#FDA7DF"},
		{"Samantha", "#FF6EC7"},
		{"Morty Jr.", "#12CBC4"},
		{"Gazorpazorp Jr.", "#C44569"},
		{"Purple Morty", "#A29BFE"},
		{"My Daughter's Boyfriend", "#576574"},
		{"Alan Rails", "#E1DA00"},
		{"Lady Katana", "#C44569"},
		{"Slippery Stair", "#7B8C86"},
		{"Gary", "#4B6584"},
		{"Logg", "#7B8C86"},
		{"Ugly Log", "#576574"},
		{"Forest", "#10A881"},
		{"S-worm", "#2ED573"},
		{"Dark Morty", "#2C2C54"},
		{"Giant Incest Baby", "#FDA7DF"},
		{"Million Ants", "#FF6B81"},
		{"Eyepatch Morty", "#54A0FF"},
		{"Warlock", "#A29BFE"},
		{"Human-man", "#34ACE0"},
		{"Brick", "#E77F67"},
		{"Armagheadon", "#C44569"},
		{"Blue Footprint Guy", "#12CBC4"},
		{"Planetina's Manager", "#576574"},
		{"Alan's Son", "#E1DA00"},
		{"Group of Flies", "#7B8C86"},
		{"Half-Eye Guy", "#576574"},
		{"Shrimply Pibbles", "#E77F67"},
		{"Demon", "#C44569"},
		{"Cult Leader", "#2C2C54"},
		{"Crystal Monster", "#9AECDB"},
		{"Deer Teacher", "#10A881"},
		{"Deer Principal", "#2ED573"},
		{"Eric Stoltz Mask Eyed Morty", "#706FD3"},
		{"Garmanarnar", "#576574"},
		{"Guard", "#B3B1AD"},
		{"Jessica's Friend", "#FDA7DF"},
		{"Scropon", "#5758BB"},
		{"Lighthouse Keeper", "#4B6584"},
		{"Coop", "#EAB54C"},
		{"Courier", "#576574"},
		{"Dalek Doctor", "#5758BB"},
		{"Donna", "#FDA7DF"},
		{"Franchise Tom", "#576574"},
		{"Glorzo", "#9AECDB"},
		{"Gulps", "#7B8C86"},
		{"Hairs", "#4B6584"},
		{"Headana", "#E15F41"},
		{"Hotch", "#EE5A24"},
		{"Inside Guy", "#576574"},
		{"Invisi-Trooper", "#2C2C54"},
		{"Judge Morty", "#12CBC4"},
		{"Loop", "#A29BFE"},
		{"Man Painted Like Statue", "#FDA7DF"},
		{"Ma-Sha", "#E1DA00"},
		{"Mr. Always Wants to be Hunted", "#576574"},
		{"Narrator", "#485460"},
		{"Nightmare", "#2C2C54"},
		{"Nowhere King", "#576574"},
		{"Party Morty", "#FDA7DF"},
		{"Party Rick", "#00D4AA"},
		{"Pizza", "#FDA7DF"},
		{"Robot Rick", "#576574"},
		{"Robot Morty", "#12CBC4"},
		{"Rondon", "#576574"},
		{"Scroopy Noopers' Friend", "#1B9CFC"},
		{"Shleemypockets", "#706FD3"},
		{"Shmlon", "#EAB54C"},
		{"Shnoopy Bloopers", "#FDA7DF"},
		{"Sperm Monster", "#FF6B81"},
		{"Spooky", "#2C2C54"},
		{"Spoon Person", "#34ACE0"},
		{"Steve", "#EAB54C"},
		{"Stop Motion", "#576574"},
		{"Story Lord", "#9AECDB"},
		{"Story Train", "#706FD3"},
		{"Toaster", "#34ACE0"},
		{"Traflorkian", "#9AECDB"},
		{"Tree", "#10A881"},
		{"Tumblork", "#5758BB"},
		{"Tusked Creature", "#4B6584"},
		{"Velvet", "#FF3838"},
		{"Ventriloquist", "#34ACE0"},
		{"Voltamatron", "#5352ED"},
		{"Wedding Singer", "#FDA7DF"},
		{"Welcome Demon", "#2C2C54"},
		{"Zeep Xanflorp", "#5352ED"},
	}
}

// Assign gives a unique agent name and color from the pool.
func Assign() AgentColor {
	mu.Lock()
	defer mu.Unlock()

	if len(inUse) >= len(pool) {
		inUse = map[string]struct{}{}
	}

	var choice AgentColor
	for attempts := 0; attempts < len(pool)*2; attempts++ {
		choice = pool[rng.Intn(len(pool))]
		if _, taken := inUse[choice.Name]; !taken {
			break
		}
	}

	inUse[choice.Name] = struct{}{}
	return choice
}

// AssignAt returns a deterministic character for ordered team rendering.
func AssignAt(index int) AgentColor {
	mu.Lock()
	defer mu.Unlock()
	if len(pool) == 0 {
		return AgentColor{Name: "Teammate", Color: "#00D4AA"}
	}
	if index < 0 {
		index = 0
	}
	return pool[index%len(pool)]
}

// Release returns a name back to the pool.
func Release(name string) {
	mu.Lock()
	defer mu.Unlock()
	delete(inUse, name)
}

// Peek returns the current in-use names (for testing).
func Peek() []string {
	mu.Lock()
	defer mu.Unlock()
	names := make([]string, 0, len(inUse))
	for n := range inUse {
		names = append(names, n)
	}
	return names
}
