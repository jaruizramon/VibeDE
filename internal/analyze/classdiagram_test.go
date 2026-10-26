package analyze

import (
	"testing"

	"vibede/internal/diagram"
)

func TestClassGraphUsageEdgesAcrossFiles(t *testing.T) {
	gameSrc := []byte(`class Game {
  spawn() {
    // Laser is handled elsewhere; this comment must not create an edge.
    const block = new Block();
    this.particles.push(new Particle());
  }
}
`)
	blockSrc := []byte(`class Block {
  update() {}
}

class Particle {
  update() {}
}

class Laser {
  update() {}
}
`)
	game, err := analyzeGeneric("js/game.js", "javascript", gameSrc)
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := analyzeGeneric("js/objects.js", "javascript", blockSrc)
	if err != nil {
		t.Fatal(err)
	}
	g := buildClassGraph([]classFile{
		{Path: "js/game.js", Src: gameSrc, A: game},
		{Path: "js/objects.js", Src: blockSrc, A: blocks},
	}, 100)

	ids := map[string]string{}
	for _, n := range g.Nodes {
		ids[n.Label] = n.ID
	}
	if ids["Game"] == "" || ids["Block"] == "" || ids["Particle"] == "" || ids["Laser"] == "" {
		t.Fatalf("nodes = %+v", g.Nodes)
	}
	foundBlock, foundParticle, foundLaser := false, false, false
	for _, e := range g.Edges {
		if e.From != ids["Game"] {
			continue
		}
		if e.To == ids["Block"] && e.Kind == diagram.Dependency && e.Label == "uses" {
			foundBlock = true
		}
		if e.To == ids["Particle"] && e.Kind == diagram.Dependency && e.Label == "uses" {
			foundParticle = true
		}
		if e.To == ids["Laser"] {
			foundLaser = true
		}
	}
	if !foundBlock || !foundParticle {
		t.Fatalf("missing usage edges: %+v", g.Edges)
	}
	if foundLaser {
		t.Fatalf("comment-only mention created a Laser edge: %+v", g.Edges)
	}
	for _, e := range g.Edges {
		if e.From == e.To {
			t.Fatalf("self edge: %+v", e)
		}
	}
}

func TestClassGraphStructuralEdges(t *testing.T) {
	src := []byte(`class Base {
  base() {}
}
class Child extends Base {
  child() {}
}
`)
	a, err := analyzeGeneric("js/app.js", "javascript", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Types) < 2 {
		t.Fatalf("types = %+v", a.Types)
	}
	g := buildClassGraph([]classFile{{Path: "js/app.js", Src: src, A: a}}, 100)
	ids := map[string]string{}
	for _, n := range g.Nodes {
		ids[n.Label] = n.ID
	}
	found := false
	for _, e := range g.Edges {
		if e.From == ids["Child"] && e.To == ids["Base"] && e.Kind == diagram.Inherit {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing extends edge: %+v", g.Edges)
	}
}
