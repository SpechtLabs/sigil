//! Kinds that cover every corner of `schema()`, mirrored one for one in
//! `bindings/typescript/test/testdata/gokinds/main.go`, which prints what Go's
//! `Kind.Schema` writes for them, plus the example services' kinds, whose
//! kind files their Go hosts exported.
#![allow(dead_code)]

use serde_json::json;
use sigil::{Decision, FnDecl, Kind, OutcomeRef, Type, host_fn};

// examples/alert-routing: internal/routing/kind.go in the Go service.
pub fn alert_routing() -> Kind {
    let severity = Type::enumeration("Severity", ["critical", "warning", "info"]);
    let alert = Type::structure(
        "Alert",
        [
            ("name", Type::string()),
            ("severity", severity),
            ("labels", Type::map(Type::string(), Type::string())),
            ("firing_for", Type::duration()),
        ],
    );
    let team = Type::structure("Team", [("name", Type::string()), ("oncall", Type::string()), ("channel", Type::string())]);
    let page = Decision::new("page", ["critical_alert", "sustained"]).field("target", Type::string());
    let drop = Decision::new("drop", ["muted", "not_production"]);
    let notify = Decision::new("notify", ["routine", "unrouted"]).field_default("channel", Type::string(), json!("#alerts"));
    Kind::builder("AlertRouting")
        .version(1)
        .input("alert", alert)
        .input("team", team)
        .decisions([&page, &drop, &notify])
        .rank_reasons(&page)
        .rank_reasons(&drop)
        .rank_reasons(&notify)
        .default_outcome(notify.reason("unrouted"))
        .build()
        .unwrap()
}

pub fn deploy_decisions() -> (Decision, Decision, Decision) {
    (
        Decision::new("deny", ["not_eligible", "change_freeze", "soak_too_short", "no_rule_matched"]),
        Decision::new("review", ["service_owner"]).field("approvers", Type::list(Type::string())),
        Decision::new("approve", ["release_manager", "payments_sre"]).field_default("bake", Type::duration(), json!("1h")),
    )
}

// examples/deploy-gates: internal/deploy/kind.go.
pub fn deploy_approval() -> Kind {
    let tier = Type::enumeration("Tier", ["critical", "standard", "internal"]);
    let release = Type::structure("Release", [("soak", Type::duration()), ("hotfix", Type::bool())]);
    let service = Type::structure(
        "Service",
        [
            ("name", Type::string()),
            ("tier", tier),
            ("owners", Type::list(Type::string())),
            ("labels", Type::map(Type::string(), Type::string())),
        ],
    );
    let actor = Type::structure(
        "Actor",
        [
            ("name", Type::string()),
            ("teams", Type::list(Type::string())),
            ("roles", Type::list(Type::string())),
            ("regions", Type::list(Type::string())),
        ],
    );
    let freeze = Type::structure("Freeze", [("environments", Type::list(Type::string())), ("unknown", Type::bool())]);
    let (deny, review, approve) = deploy_decisions();
    Kind::builder("DeployApproval")
        .version(2)
        .input("release", release)
        .input("service", service)
        .input("actor", actor)
        .input("environment", Type::string())
        .input("freeze", freeze)
        .function(
            "split",
            FnDecl::new([Type::string(), Type::string()], Type::list(Type::string())).implement(host_fn(|args| {
                let (Some(s), Some(sep)) = (args.first().and_then(|v| v.as_str()), args.get(1).and_then(|v| v.as_str())) else {
                    return Err("split wants two strings".into());
                };
                Ok(json!(s.split(sep).collect::<Vec<_>>()))
            })),
        )
        .decisions([&deny, &review, &approve])
        .rank_reasons(&deny)
        .rank_reasons(&approve)
        .default_outcome(deny.reason("no_rule_matched"))
        .build()
        .unwrap()
}

// examples/deploy-gates: internal/access/kind.go.
pub fn access_grant() -> Kind {
    let actor = Type::structure("Actor", [("name", Type::string()), ("groups", Type::list(Type::string())), ("clearance", Type::string())]);
    let reader = Decision::new("reader", ["team_member", "everyone_in_staging"]);
    let deployer = Decision::new("deployer", ["team_member", "oncall"]).field_default("ttl", Type::duration(), json!("8h"));
    let release_manager = Decision::new("release_manager", ["platform_member"]).field_default("ttl", Type::duration(), json!("8h"));
    let admin = Decision::new("admin", ["clearance", "break_glass"]).field_default("ttl", Type::duration(), json!("1h"));
    let auditor = Decision::new("auditor", ["compliance_member"]);
    Kind::builder("AccessGrant")
        .version(1)
        .input("actor", actor)
        .input("team", Type::string())
        .input("environment", Type::string())
        .collect_all([&reader, &deployer, &release_manager, &admin, &auditor])
        .exclusive([&admin, &release_manager])
        .build()
        .unwrap()
}

pub fn everything() -> Kind {
    let level = Type::enumeration("Level", ["low", "high"]);
    let region = Type::enumeration("Region", ["eu", "us"]);
    let unused = Type::enumeration("Unused", ["a", "b"]);
    let mood = Type::enumeration("Mood", ["calm", "tense"]);
    let group = Type::structure("Group", [("name", Type::string()), ("admin", Type::bool())]);
    let user = Type::structure("User", [("name", Type::string()), ("groups", Type::list(group)), ("level", level.clone())]);
    let empty = Type::structure("Empty", Vec::<(String, Type)>::new());
    let site = Type::structure("Site", [("name", Type::string())]);
    let request = Type::structure(
        "Request",
        [
            ("user", user.clone()),
            ("manager", Type::optional(user.clone())),
            ("tags", Type::list(Type::string())),
            ("meta", Type::map(Type::string(), Type::int())),
            ("deadline", Type::timestamp()),
            ("score", Type::float()),
            ("empty", empty),
        ],
    );
    let deny = Decision::new("deny", ["bad", "worse", "fallback"]);
    let allow = Decision::new("allow", ["fine", "great"])
        .field_default("ttl", Type::duration(), json!("90m"))
        .field_default("days", Type::duration(), json!("2d3h"))
        .field_default("note", Type::string(), json!("tab\t \"quoted\" \\ ✓ é 😀 \u{1} \u{2028} \u{7f}"))
        .field_default("limit", Type::int(), json!(-3))
        .field_default("ratio", Type::float(), json!(0.5))
        .field_default("big", Type::float(), json!(1e21))
        .field_default("tiny", Type::float(), json!(1e-7))
        .field_default("whole", Type::float(), json!(2))
        .field_default("on", Type::bool(), json!(true))
        .field_default("tags", Type::list(Type::string()), json!(["b", "a"]))
        .field_default("level", level, json!("high"))
        .field_default("limits", Type::map(Type::string(), Type::int()), json!({"b": 2, "a": 1, "ä": 3, "Z": 4}))
        .field_default("maybe2", Type::optional(Type::int()), json!(4))
        .field_default("empty", Type::list(Type::string()), json!([]));
    let hold = Decision::new("hold", ["wait"]).field("until", Type::string()).field("mood", mood.clone());
    Kind::builder("Everything")
        .version(3)
        .accepts(2)
        .enumeration(unused)
        .enumeration(mood)
        .enumeration(region.clone())
        .enumeration(Type::enumeration("Level", ["low", "high"]))
        .input("request", request)
        .input("now", Type::timestamp())
        .input("limit", Type::optional(Type::int()))
        .input("weights", Type::map(Type::string(), Type::float()))
        .function("lookup", FnDecl::new([Type::string(), Type::map(region.clone(), Type::int())], user))
        .function(
            "count",
            FnDecl::new([Type::list(Type::string())], Type::int())
                .implement(host_fn(|args| Ok(json!(args[0].as_array().map_or(0, Vec::len))))),
        )
        .function("locate", FnDecl::new([site], region).implement(host_fn(|_| Ok(json!("eu")))))
        .decisions([&deny, &allow, &hold])
        .rank_outcomes([deny.reason("worse"), deny.reason("bad"), deny.reason("fallback")])
        .exclusive([OutcomeRef::from(allow.reason("great")), OutcomeRef::from(&hold)])
        .default_outcome(deny.reason("fallback"))
        .conflict(deny.reason("worse"))
        .build()
        .unwrap()
}

pub fn collecting() -> Kind {
    let a = Decision::new("a", ["r1", "r2"]);
    let b = Decision::new("b", ["x", "y"]).field_default("weight", Type::float(), json!(1));
    let c = Decision::new("c", ["z"]);
    Kind::builder("Collecting")
        .version(1)
        .input("items", Type::list(Type::string()))
        .collect_all([&a, &b, &c])
        .precedence([&c, &a, &b])
        .rank_outcomes([a.reason("r2"), a.reason("r1")])
        .exclusive([OutcomeRef::from(&a), OutcomeRef::from(b.reason("y"))])
        .exclusive([&b, &c])
        .default_outcome(c.reason("z"))
        .build()
        .unwrap()
}

pub fn minimal() -> Kind {
    let ok = Decision::new("ok", ["yes"]);
    Kind::builder("Minimal")
        .version(1)
        .input("who", Type::string())
        .function(
            "upper",
            FnDecl::new([Type::string()], Type::string())
                .implement(host_fn(|args| Ok(json!(args[0].as_str().unwrap_or_default().to_uppercase())))),
        )
        .decisions([&ok])
        .default_outcome(ok.reason("yes"))
        .build()
        .unwrap()
}

pub fn access_decisions() -> Vec<Decision> {
    vec![
        Decision::new("reader", ["team_member", "everyone_in_staging"]),
        Decision::new("deployer", ["team_member", "oncall"]).field_default("ttl", Type::duration(), json!("8h")),
        Decision::new("release_manager", ["platform_member"]).field_default("ttl", Type::duration(), json!("8h")),
        Decision::new("admin", ["clearance", "break_glass"]).field_default("ttl", Type::duration(), json!("1h")),
        Decision::new("auditor", ["compliance_member"]),
    ]
}
