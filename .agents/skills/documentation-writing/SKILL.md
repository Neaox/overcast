---
name: documentation-writing
description: "Writes and restructures Overcast documentation so readers can actually follow it — README, docs/, docs/dev/, docs/plans/, service pages, design docs and architecture material. Covers what a reader can be assumed to know, the four documentation modes and why mixing them fails, ordering and structure for scannability, which claims can be made and how to signal an inference rather than a fact, when a metaphor earns its place, and how to explain a limitation or a surprising behaviour without editorialising. Use whenever writing, restructuring or reviewing Markdown in this repository, when a doc is accurate but hard to follow, when deciding where a piece of writing belongs, or when explaining a mechanism to someone who does not already know it. Not for verifying documentation against the code, which is documentation-audit."
argument-hint: "Doc path, topic, or the thing being explained"
---

# Documentation Writing — Overcast

How to write documentation people can actually use. For verifying an existing doc set against the code, use `documentation-audit` — this skill is about getting it right the first time.

A document can be entirely accurate and still fail: by assuming knowledge the reader lacks, by burying the load-bearing idea, by mixing four jobs into one page, or by making claims it cannot support so the reader stops trusting any of it. Those are the failures this skill addresses.

It applies to the prose in any Markdown here, including plan docs and long PR bodies. It does not cover commit messages (`commit`) or PR structure (`pull-request`).

---

## 1. Know your reader

Get this wrong in either direction and the doc fails: patronising if you over-explain, impenetrable if you under-explain. The balance is delicate and worth thinking about explicitly.

**The reliable floor.** Almost nobody reaches Overcast without these, because there would be no reason to be here:

- **They have used AWS**, in some form, and know it is a cloud provider offering many services. They have met regions and probably ARNs.
- **They program.** HTTP verbs and headers, JSON and XML, containers as a concept, async and retries, environment variables, the command line, git.
- **Basic operational literacy.** That DNS resolves names, that ports get bound, that a process listens on an interface.

Write to this floor without hedging. Explaining what a bucket is, or what JSON is, wastes the reader's time and signals you have misjudged them.

Note how thin that floor is. It is thinner than instinct suggests, and each time it gets padded out the doc starts excluding someone.

**What varies — and it is patchy, not a gradient.** The mistake is not assuming too much or too little; it is assuming *uniformity*. Knowledge here is uneven along three independent axes, and someone can sit anywhere on each.

**By depth.** Readers range from people who have only ever called AWS through an SDK to people who have implemented against its wire formats:

- **How AWS works underneath** — that an SDK call is ordinary HTTP, that there are several wire protocols, what a SigV4 header contains.
- **Go specifics** — struct tags, goroutines, `sync.Once`, build tags, `chi`.
- **Deeper container networking** — Docker's embedded resolver, network aliases, host-gateway, dual-stack binding.

**By role, which is the one that catches people out.** Which AWS services a developer knows depends on what their organisation has them do, and whole domains are routinely owned by somebody else:

- **IAM** is the clearest case. A developer may deploy to AWS daily and have never written a policy, because a platform or operations team owns credentials and permissions entirely. Policy documents, trust relationships and evaluation order are specialist knowledge, not general knowledge.
- **CloudFormation and infrastructure-as-code** likewise — plenty of developers consume infrastructure they did not define, and many have never written a template.
- **Individual services.** Someone fluent in Lambda and S3 may never have touched DynamoDB or Step Functions.

**By interface.** How someone has met AWS shapes what they take for granted:

- **Console only.** A real and easily forgotten group. They know what a bucket is because they have clicked through one, but may never have configured credentials for a client, set a region programmatically, or seen an endpoint URL. For them, "point your SDK at `localhost:4566`" is not a small adjustment — it is the entire mechanism, unfamiliar.
- **CLI**, **SDK**, and **IaC** users each arrive with different defaults about what is normal.

This matters most in getting-started material, where the console-only reader is precisely who a quickstart has to land for.

So do not treat "uses AWS" as implying any particular service, tool or workflow. Introduce service-specific concepts the way you would a Go one: briefly, and skippably.

**Do not require it, and do not belabour it.** The technique that serves both readers:

- **Gloss in a clause, not a paragraph.** "struct tags (Go's equivalent of a `@JsonProperty` annotation)" costs the expert half a second and rescues everyone else. A three-paragraph explanation of annotations insults them both.
- **Put levelling-up material in skippable units.** A clearly-labelled primer section, or a link, lets the knowledgeable reader skip cleanly. Sprinkling basics through the body forces them to wade.
- **Never gate the content behind the basics.** The reader who already knows should reach the substance without reading a tutorial first.
- **Bridge to what they do know.** Readers unfamiliar with Go usually know another language well — `@JsonProperty`, `[JsonPropertyName]` — which is faster and less condescending than first principles.

**What genuinely is absent.** Only one category can be assumed missing, because it exists nowhere else: **anything specific to this project** — its architecture, its conventions, and its own vocabulary. Two sub-cases, and they want different treatment:

- **Terms this project coined.** The emulation tiers (*full*, *partial*, *inert*, *stub*), the capability registry, the reserved `/_*` namespace, the *hot* and *cached* storage tiers. These exist nowhere else, so define them on first use and consider a glossary if a doc leans on several.
- **Standard terms used in a specific local sense.** *Wire protocol* is Smithy and AWS terminology with [its own specification](https://smithy.io/2.0/guides/wire-protocol-selection.html); *split-horizon* is ordinary networking vocabulary. Do not define these as though the project invented them — that misleads a reader who already knows the general term, and denies everyone else the pointer to a real specification. Link the standard meaning and say what is specific here: *which* hostnames Overcast treats as split-horizon, *which* wire protocols it dispatches.

Getting this backwards is easy and worth checking: before labelling a term as project-specific, confirm it is not simply a term of art you have not met. A borrowed word presented as a coinage is a small error that costs the reader a real reference.

**Adjust by destination.** `README.md` and `docs/` reach users, who want less depth. `docs/dev/` reaches contributors, where more Go is reasonable — but project vocabulary still needs defining.

**The test — two questions, and you want yes to both:**

1. Can a reader who has only ever used AWS through an SDK follow this?
2. Does a reader who already knows the internals reach the substance without wading through what they know?

If either is no, the fix is usually to *move* the explanation — into a clause, a labelled primer, or a link — rather than to add or cut it. Adding serves the first reader at the second's expense; cutting does the reverse. Moving serves both.

**Watch for words with two meanings.** "Tags" means AWS resource tags to this audience and Go struct tags to the code. Say which, every time.

---

## 2. Know which of the four kinds you are writing

Most structural failures are mode confusion. Four modes, each with a different job, and **a document should be one of them**:

| Mode | Serves | Reader is | Failure if mixed in |
|---|---|---|---|
| **Tutorial** | learning | following along, no context yet | explanation stops their momentum |
| **How-to** | a task | has a goal, wants it done | teaching wastes their time |
| **Reference** | lookup | knows what they want, needs the detail | narrative makes it unscannable |
| **Explanation** | understanding | wants the why, not doing anything | steps and detail obscure the idea |

In this repo: the README quickstart is a tutorial; `docs/sdk-cli.md` is how-to; `docs/services/**` is reference; `docs/dev/architecture.md` is explanation.

**The most common mistake is explanation leaking into a tutorial or a how-to.** A reader mid-task does not want to know why the router treats S3 as a fallback. Link it and move on.

The second most common is reference growing narrative. If a service page starts explaining *why*, that paragraph belongs in a dev doc with a link from the reference.

Accuracy failures cluster in reference; comprehension failures cluster in explanation. Most of what follows is aimed at explanation, which is the hardest to write and the least generatable.

---

## 3. Decide what the document is not about

Scope creep is how a document becomes unusable. The reader-knowledge problem in §1 makes this worse, because every gap looks like something to fill — and filling them all turns a page about Overcast into a page about AWS.

**The rule: the subject is always Overcast.** Not "how IAM works" but "how IAM works *here*". Not "what CloudFormation is" but "what Overcast does when you deploy a stack". An AWS concept appears only as far as it is needed to make an Overcast behaviour comprehensible, and no further.

| The concept is | Do this |
|---|---|
| Pure AWS, and Overcast does not change it | Link to AWS's own docs. One clause of context, no more. |
| An AWS concept where Overcast's behaviour differs, or that is needed to understand an Overcast decision | Say what Overcast does, introducing only the AWS background that sentence requires. Link out for the rest. |
| Overcast's own | Explain it properly. It exists nowhere else. |

**The test: strip every mention of Overcast from the paragraph. If what remains still stands as a general AWS explainer, it is in the wrong document.** That paragraph belongs to AWS's documentation, and a link is the right way to include it.

This shows up in headings too. "How Overcast resolves a bucket hostname" frames correctly; "S3 virtual-host addressing" invites a general essay that was never the job.

**Do not reproduce another project's documentation.** A paragraph explaining IAM policy evaluation will be worse than AWS's version, will go stale without anyone noticing, and takes on a maintenance burden that was never yours. Link it. The per-service pages already follow this pattern, linking the AWS API reference for each operation.

The judgement call is the middle row, and the question is always: **does the reader need this to understand what Overcast does?** A doc explaining that IAM enforcement is opt-in needs one sentence on what enforcement means — not a primer on policy documents, trust relationships and evaluation order.

**If several docs need the same background, extract it once.** Repeating a primer across pages is how they drift apart. Write it in one place and link. `docs/dev/architecture.md` carries the wire-protocol primer for exactly this reason — everything else links there rather than re-explaining.

**Never link a plan doc from durable documentation.** `docs/plans/` holds working documents that inform development and are **deleted once the work lands**. A link to one from a doc meant to last is a broken link waiting to happen, and worse, it implies the knowledge has a home when it is about to be thrown away.

If a plan contains something durable — a design rationale, a classification matrix, a per-service analysis — that content needs a real home before the plan goes: the relevant `docs/` page for users, `docs/dev/` for contributors, or a service page. Link *that*. For work that is merely tracked rather than explained, link the **issue**, which outlives the plan.

**Say what the doc excludes**, where it is not obvious. A sentence naming what it does not cover, and where that lives, is worth more than a section half-covering it.

---

## 4. Structure for digestibility

**Order by dependency, not by importance.** If section 5 needs a fact from section 2, section 2 comes first even if it is less interesting. `docs/dev/architecture.md` puts the wire-protocol primer second, before anything about Overcast's own design, because nothing after it parses without it.

**Front-load the idea that makes the rest work.** Usually one idea per document does most of the lifting. Find it, put it early, and say plainly that everything depends on it.

**Readers scan before they read.** So:

- **Headings must be descriptive, not clever.** "Why S3 is last" navigates; "The awkward one" does not.
- **Topic sentence first in every paragraph.** A reader skimming first sentences should get the argument.
- **A reader should be able to navigate by headings alone** and land in the right place.

**One sentence, one job.** A sentence can be accurate and unreadable if it carries three requirements joined by commas. If a reader could parse every word and still not know what to do, split it into a list and attach the *why* to each item.

**Progressive disclosure.** Mechanism, then consequence, then edge case — each skippable once the reader has what they came for. Never open with the exception.

**Pick the right form:**

| Form | Use when |
|---|---|
| Prose | there is a *why*, or the ideas connect |
| Table | two or more things vary along the same axes |
| Numbered list | order matters, or it is a procedure |
| Bulleted list | items are peers and order does not matter |
| Diagram | the relationship is spatial, branching, or a sequence over time |
| Worked example | the abstraction is hard but one instance is obvious |

Prose listing parallel attributes wants to be a table. A table with one column of paragraphs wants to be prose.

**Know when to stop.** A document that covers everything gets used for nothing. Decide what it is *not* about and link the rest.

---

## 5. Voice and mechanics

Small conventions, applied consistently, do a lot of work:

- **Present tense.** "The router dispatches", not "will dispatch". Documentation describes a system that exists.
- **Active voice**, unless the actor is genuinely irrelevant or unknown.
- **Second person for instructions**, third for description. "Set `OVERCAST_HOST`" / "Overcast binds every interface".
- **One term per concept.** Do not alternate handler / endpoint / operation for the same thing. Pick one, use it everywhere, and define it once if it is project-specific.
- **Plain English for a global audience.** Avoid idiom, phrasal verbs where a single verb exists, cultural references, and humour that depends on register. Many readers are not first-language English speakers.
- **Meaningful link text.** Link the thing, not "here" or "this page".
- **Diagrams need a text equivalent.** Anyone using a screen reader, or reading a diff, gets nothing from an image. Put the key relationship in the surrounding prose too — which is good writing regardless, since a diagram should confirm an idea rather than carry it alone.
- **Do not rely on colour alone** to distinguish anything in a diagram.

**Code examples must run.** No `...`, no pseudo-code where real code would do, no omitted imports. If it can be pasted and executed, the reader trusts everything around it. Say what each part demonstrates, and keep it minimal — every extra line is something they have to decide is irrelevant.

**Date what will age.** For anything measured, surveyed or counted, say when. "As of August 2026" costs four words and tells a future reader how much to trust it.

---

## 6. Make only claims you can support

Four kinds of statement, and confusing them is how a document loses trust:

| Kind | How to signal it | Example |
|---|---|---|
| **Verified fact** | state plainly | "`resourceHandlers` registers 132 types." |
| **Attributed claim** | name the source | "`AGENTS.md` calls this the classic mistake here." |
| **Your own inference** | mark it as reasoning | "CBOR's benefit is whole-body encoding, so a REST protocol gains little — reasoning from mechanism, not a documented decision." |
| **Speculation** | do not write it | — |

The middle two are where documents go wrong. An inference presented as fact is indistinguishable from a verified one until someone acts on it. A hedge that is *specific about what is unverified* costs nothing: "nobody has benchmarked this" beats vague softening like "generally" or "should be".

**Never claim what people think or feel.** Not "this surprises people", "most developers expect", "everyone gets this wrong". Attribute it, hedge it in the second person ("what might surprise you"), or state the underlying fact and let it land.

**An unmeasured performance number and an unsourced claim about confusion are the same defect.** Performance numbers always carry their measurement conditions — see `docs/dev/performance.md` § Documenting performance claims.

---

## 7. When you can embellish

Analogy, metaphor and framing are legitimate — sometimes essential. The test is whether the device does **explanatory work** or only aesthetic work.

**Earns its place:**

- An analogy that lets the reader predict something they were not told. "A service's wire protocol is a fossil record of when it was built" correctly predicts that a Query-protocol service is old.
- A framing that collapses a list into a rule. Five protocols as two API styles across three serialisation eras replaces memorisation with derivation.
- A concrete worked example standing in for an abstraction.

**Does not:**

- Rhetorical symmetry blurring a fact you could state. "Rules with a gate hold; rules without one mostly hold" hides "nine violations across five files".
- Drama. An emulator has no villains; a surprising behaviour is usually correct parts composing badly.
- Anything the reader must decode before extracting meaning.

**Clarity first. If a phrase also lands, keep it; if it costs a moment's comprehension, cut it.**

---

## 8. Be explicit about who acts

Three actors appear in Overcast documentation, and conflating them misleads:

1. **The reader** — running `overcast https enable`, setting a variable, trying the example.
2. **Overcast itself** — registering Docker network aliases, resolving the storage backend, deriving Lambda limits.
3. **A contributor** — adding a service, registering a CloudFormation handler.

The imperative mood belongs **only** to the first. Writing "attach the hostname as a network alias" about something Overcast does automatically tells the reader they have missed a step that does not exist.

Test each instruction: *who does this, and when?* If the answer is "Overcast, always", say so — and say when it becomes the reader's problem, if ever.

**Do not describe configuration as risk.** An explicit setting doing what it says is a feature; name why someone would want it. A concerning *default* is a different thing and belongs in an issue.

---

## 9. Explain limitations, do not just declare them

"We do not do X" leads a reader nowhere. Which kind it is changes what they do next:

| Kind | Say | Reader's next move |
|---|---|---|
| Permanent design decision | why the alternative is worse | accept it |
| Technical constraint | what constrains it | find another approach |
| Deliberate trade | the benefit **and** the named cost | watch for the cost |
| Not done yet | link the issue | pick it up |

**A complaint in documentation is a defect that has been described rather than filed** — either link an issue or do not write the sentence.

---

## 10. Explain surprises, do not editorialise

When something is counter-intuitive, distinguish a design flaw from an emergent consequence of correct parts. In an emulator it is almost always the latter.

Say what constrains the alternatives. "Overcast's resolver answers for a name that should have been a container, so the client hangs" reads as bad design — until you add that the resolver must be authoritative for that domain, cannot distinguish the two cases, and that forging `NXDOMAIN` would be worse because clients cache negative answers. Same facts, opposite impression, and only the second version tells the reader where the bug actually is.

---

## 11. Explain and link; do not enumerate

Prose should not carry lists that change: resource types, environment variables, operation counts, supported-service tables. Every hand-written enumeration is a copy of something that will change without it.

Link the generated source instead. If a number genuinely belongs in a sentence, prefer a shape that ages well.

This is a *writing* decision preventing a *maintenance* problem — `documentation-audit` covers what happens when it is ignored.

---

## Common failure patterns

Recognise these in your own draft and in review:

| Pattern | Looks like | Fix |
|---|---|---|
| **Mode mixing** | a how-to that pauses to explain architecture | move the explanation out, link it |
| **Curse of knowledge** | a term used, then a consequence of it explained | gloss the term, or cut the consequence |
| **The buried lede** | the key idea in paragraph six | move it to paragraph one |
| **Enumeration rot** | a hand-typed count or list | link the generated source |
| **Imperative drift** | instructions for work Overcast does itself | say who acts |
| **Unattributed authority** | "this is confusing", "most people" | attribute, hedge, or cut |
| **The naked limitation** | "X is not supported" with no category | say which kind, link an issue |
| **Sentence overload** | three requirements joined by commas | split into a list |
| **Decorative prose** | a phrase that must be decoded | cut it |
| **Self-contradiction** | two sections disagreeing | read end to end, every time |

---

## Where it goes, and the mechanics that follow

| Location | Audience | Indexed & shipped | Changelog gate |
|---|---|---|---|
| `README.md` | users evaluating or installing | — | applies |
| `docs/*.md` | users | yes — run `make docs-index` | applies |
| `docs/services/*.md` | users, per-service landing page | yes — the Operations stub | applies, plus `docs/dev/service-doc-template.md` |
| `docs/services/*/operations.md` | users, per-operation reference | fully generated by `cmd/capgen` | do not hand-edit |
| `docs/dev/*.md` | contributors | **no** | exempt |
| `docs/plans/*.md` | working design docs | **no** | exempt |
| `CONTRIBUTING.md` | all contributors | — | applies |
| `AGENTS.md` | agents specifically | — | applies |
| `.agents/skills/*` | agents | — | exempt |

- **Editing a published doc means running `make docs-index` and committing the result.** CI fails otherwise. `docs/dev/` and `docs/plans/` are skipped by the indexer.
- **An anchor into a published doc uses the docs browser's slug, not GitHub's.** A run of non-alphanumerics collapses to ONE hyphen there, so "HTTPS / TLS" is `#https-tls` and "Data dir placement — avoid …" is `#data-dir-placement-avoid-…`; GitHub's slugger would keep the doubled hyphen. Anchors into `docs/dev/`, `docs/plans/`, `AGENTS.md` and the root README are read on GitHub and keep GitHub's form. `make docs-check` validates the published side against the real heading ids and names the id you meant.
- **A Markdown-only change needs no test run.**
- **Published docs and root files are in changelog scope** — add a fragment or comment `/no-changelog docs-only: …` on the PR, **before** waiting on checks.
- **`AGENTS.md` is only for what matters because an agent is executing autonomously.** If a human would need it too, it belongs in `CONTRIBUTING.md` with a link from `AGENTS.md`.
- **`docs/plans/` must be accurate as of every commit.** A PR that lands behaviour must flip the status of any plan doc it satisfies, whether or not that plan is why the PR exists.

---

## Before you finish

- [ ] Is this one mode — tutorial, how-to, reference or explanation — and not a blend?
- [ ] Could a reader who knows AWS but not its internals follow it without stopping?
- [ ] Is the load-bearing idea early, and does nothing depend on a fact introduced later?
- [ ] Do the headings alone navigate the document?
- [ ] Does each sentence do one job?
- [ ] Is every claim marked for what it is — fact, attributed, or inference?
- [ ] No claims about what people think, feel or expect.
- [ ] Every instruction clear about who acts.
- [ ] Every limitation categorised; every "not done yet" links an issue.
- [ ] Do the examples actually run?
- [ ] Does any embellishment do explanatory work?
- [ ] Enumerations replaced with links to generated sources.
- [ ] **Read it end to end for internal contradictions** — a long document will contradict itself, and only a full pass finds it.
- [ ] Anchors and relative links resolve.
- [ ] `make docs-index` run if a published doc changed; changelog question answered.

---

## Related skills

- `documentation-audit` — verifying an existing doc set against the code.
- `pull-request` — the changelog gate and PR mechanics.
- `github-issue-lifecycle` — filing the issues this skill tells you to link.
