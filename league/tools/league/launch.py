# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""Restic League V2 launcher: season config, box staging, canary, dry-run, start and resume.

The mature league controller (tools/pylint_v2.py with the ResticTarget adapter) runs ON the league box from its own
controller root, isolated from the paused pylint league:

    /opt/league/restic-league-v2/league/            controller ROOT (tools/, shared/, evidence/, runtime/)
        space-bunny/restic-v2/                             league home (briefs, league.json, knowledge/)
        space-bunny/restic-v2/workspace/                   roster.json, profiles/, upstream/ (= restic stock), teams/

Usage (from LEAGUES_HOME):
    python restic-v2/launch.py --season S001 --dry-run       local config + checks only, no box access
    python restic-v2/launch.py --season S001 --stage         ship controller + briefs, prepare the workspace
    python restic-v2/launch.py --season S001 --canary        stage, then one real space-bunny role turn on the box
    python restic-v2/launch.py --season S001 --box-dry-run   stage, then construct the Season on the box and check it
    python restic-v2/launch.py --season S001 --start         stage, then start the season controller (coordinator only)
    python restic-v2/launch.py --season S001 --resume        ship current tools/config, restart from checkpoints

Transports: --transport space-bunny (default; OpenRouter stealth, effort max, free, 1,000 requests/day, pauses at
the daily cap) or --transport luna6 (GPT-6 Luna max through Codex CLI on the box, for later four-team seasons;
the box CLI refused the model name on 2026-09-24, see CONTROLLER.md).
"""
import argparse
import json
import pathlib
import sys
import tarfile
import time

HERE = pathlib.Path(__file__).resolve().parent
LEAGUES = HERE.parent
sys.path.insert(0, str(LEAGUES / 'tools'))

BOX_ROOT = '/opt/league/restic-league-v2'
CONTROLLER = BOX_ROOT + '/league'
LEAGUE_ID = 'space-bunny/restic-v2'
HOME = CONTROLLER + '/' + LEAGUE_ID
WORK = HOME + '/workspace'
PYLINT_WORK = '/opt/league/Leagues/fireworks-deepseek/pylint-v5/workspace'
PYLINT_SHARED = '/opt/league/Leagues/shared'
UPLOAD_ROOT = '/opt/league/pylint-league-v2-20260909/inputs'
SPACE_BUNNY = 'stealth/space-bunny-alpha'
LUNA6 = 'gpt-6-luna'

TOOLS = ['pylint_v2.py', 'league_targets.py', 'agent_api_runner.py', 'api_routes.py', 'run_season.py',
         'match_continuity.py', 'role_backends.py', 'pylint_judge.py', 'remote_broker.py', 'remote_lanes.py',
         'source_snapshot.py', 'common_guard.py', 'agent_runner.py', 'agent_native_runner.py',
         'agent_codex_runner.py', 'agent_claude_runner.py', 'league_bridge.py', 'league_mcp_server.py',
         'persona_pack.py', 'roster_rotation.py']
BRIEFS = ['PROTOCOL.md', 'PROBLEM-BRIEF.md', 'SME-BRIEF.md', 'LEAGUE-CARD.md']
DIRECTIVE_START = '<!-- SEASON-DIRECTIVE:START -->'
DIRECTIVE_END = '<!-- SEASON-DIRECTIVE:END -->'
DIRECTIVE_BRIEFS = ['PROBLEM-BRIEF.md', 'SME-BRIEF.md', 'LEAGUE-CARD.md']


def set_directive(text):
    """Replace the delimited season-directive block in the briefs every role reads (owners, coaches, SMEs, builders).
    The block is replaced, never appended, so one season's aim is not silently still in force the next season."""
    for name in DIRECTIVE_BRIEFS:
        path = HERE / name
        body = path.read_text(encoding='utf-8')
        nl = chr(10)
        block = (DIRECTIVE_START + nl + (('## Season directive (operator, verbatim)' + nl + nl + text.strip() + nl)
                                         if text.strip() else '') + DIRECTIVE_END)
        if DIRECTIVE_START in body and DIRECTIVE_END in body:
            start = body.index(DIRECTIVE_START); end = body.index(DIRECTIVE_END) + len(DIRECTIVE_END)
            body = body[:start] + block + body[end:]
        else:
            body = body.rstrip() + nl + nl + block + nl
        path.write_text(body, encoding='utf-8')
SCRIPTS = ['canary_role.py', 'dry_run_box.py']

SPACE_BUNNY_TRANSPORT = {
    'backend': 'openrouter', 'required_model': 'Space Bunny Alpha (OpenRouter stealth, effort max)',
    'model_id': SPACE_BUNNY, 'role_effort': {'default': 'max'}, 'max_tokens': 128000,
    # Solo season: at most three builders or inventors are ever in flight; 4 leaves room for a consultation.
    'max_parallel_roles': 4, 'transient_retries': 20, 'transient_retry_seconds': 15,
    'role_timeout_seconds': 2400, 'builder_timeout_seconds': 5400, 'experiment_builder_timeout_seconds': 3600,
    'final_builder_timeout_seconds': 4800, 'handoff_timeout_seconds': 1200, 'sync_builder_timeout_seconds': 1800,
    'finish_timeout_seconds': 4800, 'owner_address_timeout_seconds': 2400, 'consultation_timeout_seconds': 600,
    'source_check_timeout_seconds': 1800,
    'free_tier_note': ('The key allows 1,000 free-model requests per UTC day. agent_api_runner recognises OpenRouter\'s '
                       'free-models-per-day 429, records a shared pause marker, waits until X-RateLimit-Reset (or the '
                       'next UTC midnight) plus 90 s, extends the role\'s clock by the wait and resends the same '
                       'request; per-minute 429s wait for their reset. Remaining quota is logged from GET /api/v1/key.'),
    'route_change': ('Operator decision 2026-09-24: the first Restic League V2 season runs SOLO on OpenRouter '
                     'stealth/space-bunny-alpha at reasoning effort max (probed 2026-09-24: $0, 1M context, 512k max '
                     'output, tool calls work, effort max accepted).'),
    'adapter_status': 'openrouter chat-completions path (agent_api_runner); space-bunny canary on the league box, see CONTROLLER.md.',
}
LUNA6_TRANSPORT = {
    'backend': 'codex-cli', 'required_model': 'GPT-6 Luna (max)', 'model_id': LUNA6, 'role_effort': {'default': 'max'},
    'max_tokens': None, 'max_parallel_roles': 16,
    # 2026-09-25 operator: tighter builder limits (slowest final build gated every match at up to 75 min).
    'role_timeout_seconds': 3600, 'builder_timeout_seconds': 3000, 'experiment_builder_timeout_seconds': 3000,
    'final_builder_timeout_seconds': 3000, 'handoff_timeout_seconds': 1200, 'sync_builder_timeout_seconds': 1800,
    'finish_timeout_seconds': 5400, 'owner_address_timeout_seconds': 3600, 'consultation_timeout_seconds': 600,
    'source_check_timeout_seconds': 1800,
    'route_change': ('Later Restic V2 seasons: four teams on GPT-6 Luna at max effort through Codex CLI on the league box '
                     '(operator subscription). League tools reach the controller over the per-role MCP bridge.'),
    'adapter_status': ('codex exec + MCP bridge (as the pylint Luna/Astra/Terra seasons). 2026-09-24: box CLI updated '
                       '0.153.4 -> 0.156.1 (`codex update`); `codex exec -m gpt-6-luna` answered at effort low and max.'),
}
SOL61 = 'gpt-6.1-sol'
# Operator 2026-09-30: "update the CLI and run the next season on GPT 6.1 Sol". Same limits as Luna; the
# controller's role-timeout-resume covers a slower model.
SOL61_TRANSPORT = dict(LUNA6_TRANSPORT, required_model='GPT-6.1 Sol (high)', model_id=SOL61,
    role_effort={'default': 'high'},  # operator 2026-09-30: "Make it 6.1 High"
    # Operator 2026-10-01 ("Do so"): Sol hit the 3000 s builder limit 16 times in S012 M001-M003.
    builder_timeout_seconds=4500, experiment_builder_timeout_seconds=4500, final_builder_timeout_seconds=4500,
    route_change=('From S012: four teams on GPT-6.1 Sol at high effort through Codex CLI on the league box (operator '
                  'subscription). League tools reach the controller over the per-role MCP bridge.'),
    adapter_status=('codex exec + MCP bridge. 2026-09-30: box CLI updated 0.156.1 -> 0.159.2 (`codex update`); '
                    '`codex exec -m gpt-6.1-sol` answered (gpt-6-1-sol is rejected for ChatGPT accounts).'))
# Operator 2026-10-03: "Swap to using 6.1 Sol Medium mid-season and for all seasons moving forward until I request a
# stop." Limits as the Sol high transport, except the builder cap below.
SOL61M_TRANSPORT = dict(SOL61_TRANSPORT, required_model='GPT-6.1 Sol (medium)', role_effort={'default': 'medium'},
    # 2026-10-04, from S018: 3300 s. Usage study (research-tools/USAGE-STUDY-S017-M004.md): builders spent 83% of their
    # wall time waiting on tools; tools v2 cut a bench from 154 s to 69 s, so the 4500 s set for Sol high is slack.
    builder_timeout_seconds=3300, experiment_builder_timeout_seconds=3300, final_builder_timeout_seconds=3300,
    route_change=('From S016 mid-season: four teams on GPT-6.1 Sol at medium effort through Codex CLI on the league box '
                  '(operator subscription). League tools reach the controller over the per-role MCP bridge.'))
TRANSPORTS = {'space-bunny': SPACE_BUNNY_TRANSPORT, 'luna6': LUNA6_TRANSPORT, 'sol61': SOL61_TRANSPORT,
              'sol61m': SOL61M_TRANSPORT}


# Judge null re-run 2026-10-03 (judge/NULL-20261003.md): per-round paired % SD between identical arms on the main
# workload, after time-only scoring and the performance governor. Probe bands (noise_aware_probes) use these.
NULL_20261003 = {
    # Official lanes: time SD 0.27 (72 values). Research lanes (where probes run): robust SD 0.40 (stock 0.34,
    # incumbent 0.8 on 12 values) plus one ~2.5 s stall in each 12-round job. Probe values = the larger reading, rounded up.
    'time': 0.5, 'rss': 2.5,
    'phase': {'backup-initial': {'time_saved_pct': 0.9, 'rss_saved_pct': 5.9},
              'backup-unchanged': {'time_saved_pct': 0.6, 'rss_saved_pct': 6.2},
              'backup-mutated': {'time_saved_pct': 1.0, 'rss_saved_pct': 6.9},
              'restore': {'time_saved_pct': 0.8, 'rss_saved_pct': 5.5},
              'check-read-data': {'time_saved_pct': 0.8, 'rss_saved_pct': 6.0},
              'forget-prune': {'time_saved_pct': 1.35, 'rss_saved_pct': 5.4},
              'ls-json': {'time_saved_pct': 0.3, 'rss_saved_pct': 1.6}},
    'source': ('restic-v2/judge/NULL-20261003.md (identical-arm null re-run 2026-10-03, main workload, 12-round '
               'jobs on the official and research lanes); the other workloads use the same per-phase values. A '
               'single phase reading that is off by seconds is a host stall, not a result: rerun it.'),
}


# Remote workload (profile v2) null, 2026-10-05 boundary (evidence/restic-remote-v2-boundary/NULL-REMOTE-V2.md, judge/REMOTE.md
# section 12; jobs a76aa132 stock x3, bd62a706 incumbent x2, official lanes). Pooled per-round paired time SD between
# identical arms 0.42% (48 pairs); stock pairs 0.37, incumbent pairs 0.58. The TIME bands below are from the 12
# incumbent-vs-incumbent pairs alone (coordinator 2026-10-05), because a probe compares two league trees and the league
# tree is two-valued on this workload where stock is not: restore 10.35 or 10.67 s, check-read-data 6.70 or 7.00 s from
# round to round (incumbent-pair SD 1.70 and 2.56; stock-pair SD 0.20 and 0.49). Band = the larger of SD and robust SD
# x 1.5 (research lanes), rounded up; each is at least the pooled value the report prints (0.7; 1.35 / 0.6 / 4.05 /
# 1.3 / 2.15 / 3.0 / 0.6). RSS bands are the pooled ones.
NULL_REMOTE_V2 = {
    'time': 0.95, 'rss': 2.4,
    'phase': {'backup-initial': {'time_saved_pct': 1.4, 'rss_saved_pct': 5.0},
              'backup-unchanged': {'time_saved_pct': 0.6, 'rss_saved_pct': 7.0},
              'backup-mutated': {'time_saved_pct': 5.8, 'rss_saved_pct': 6.9},
              'restore': {'time_saved_pct': 2.55, 'rss_saved_pct': 4.4},
              'check-read-data': {'time_saved_pct': 3.85, 'rss_saved_pct': 6.2},
              'forget-prune': {'time_saved_pct': 3.15, 'rss_saved_pct': 7.3},
              'ls-json': {'time_saved_pct': 1.05, 'rss_saved_pct': 1.7}},
    'source': ('restic-v2/judge/REMOTE.md section 12 (identical-arm null on the official lanes, workload '
               'restic-v2-workload-remote-2, 2026-10-05): time values from the 12 incumbent-vs-incumbent pairs, because '
               'a probe compares two league trees and the league tree varies more from run to run than stock on this '
               'workload (restore and check-read-data each take one of two values); the larger of SD and robust SD '
               'x 1.5 for the research lanes, rounded up. A restore or check-read-data reading inside its band may be '
               'the same tree landing on its other value: repeat it before believing it.'),
}


# Roster rotation (operator 2026-10-05; restic-v2/roster-rotation/ROTATION.md). Seasons before ROTATION_FROM never see these
# keys (a late --resume of S018 keeps its own config). Builders: the newly written Bnn-Bnn enter before the never-played
# catalog builders (coordinator 2026-10-05), so Bnn-Bnn play S019; boundary.py apply takes the same order.
ROTATION_FROM = 'S019'
ROTATION = {'persona_additions': CONTROLLER + '/shared/roster-rotation-personas', 'owner_bench_refill': True,
            'builder_rotation': {'entry_order': ['new', 'unplayed', 'returning']}, 'coach_rotation': True,
            'inventor_rotation': True, 'inventor_attribution': True, 'roster_form_tables': True}


def base_config():
    """Every season-independent field; the transport and season fields are layered on top."""
    return {
        'id': LEAGUE_ID, 'target': 'restic', 'league_target': 'restic',
        'directory': HOME, 'workspace': WORK, 'remote_directory': HOME, 'remote_workspace': WORK,
        # Operator 2026-10-01 ("Return to Luna 6 Max and target speed"): from S013 the judge scores TIME only
        # (RESTIC_JUDGE_TIME_WEIGHT=1.0); peak memory is a guard (no promotion past +2% vs incumbent).
        'primary_objective': 'workload time saved (score_pct = time_saved_pct); peak memory guard +2% vs incumbent',
        'rss_guard_pct': 2.0, 'subfactor_axes': ['time'],
        'source_pins': {'restic': 'ba802d42b7294c98b62c16d1157ea3e80820c019'},
        'restic_box_root': BOX_ROOT, 'restic_judge': BOX_ROOT + '/judge/restic_judge.py',
        'restic_stock': BOX_ROOT + '/stock', 'isolation_evidence': BOX_ROOT + '/judge/isolation.json',
        'restic_phases': ['backup-initial', 'backup-unchanged', 'backup-mutated', 'restore', 'check-read-data',
                          'forget-prune', 'ls-json'],
        # Gates from the judge's null calibration (2026-09-24, equal-weight score; kept). Null re-run 2026-10-03 (time-only
        # score, performance governor; judge/NULL-20261003.md): per-round paired SD 0.27% time, 2.48% RSS on the official
        # lanes; SE of the median time 0.14% at 6 rounds, 0.10% at 12, 0.05% at 42; null false positive ~0 at 29/42.
        # From S019 (four workloads): the judge runs 44 promotion rounds (11 per workload); still 29 wins.
        'axis_regression_cap_pct': 5.0, 'promotion_policy': {'wins': 29, 'rounds': 44, 'minimum_score_pct': 0.25},
        'judge_null_calibration': {'pair_sd_score_pct': 0.27, 'pair_sd_time_pct': 0.27, 'pair_sd_rss_pct': 2.48,
                                   'median_se_pct': {'6': 0.14, '12': 0.10, '42': 0.05},
                                   'measured': '2026-10-03, main workload, official lanes, 72 paired values',
                                   'note': 'main-workload match differences under about 0.3% are within screen noise; a 1-round probe is diagnostic only'},
        # Official lanes: one CCD (CCD4 = 24-29), off pylint lane core 24. Research: CCD1 and CCD2 halves.
        # 2026-10-03: a three-workload suite job holds ~22-23 GB (measured cgroup peak; its tmpfs is charged to RAM); three at
        # once beside four research jobs and the research cache do not fit in 125 GB, so official judging runs two at a time.
        'judge_cpus': '26-29/30-33/36-39', 'research_cpu_slots': ['6-9', '12-15', '19-22', '42-45'], 'judge_parallel_lanes': 2,  # 2 from S019 (boundary 2026-10-05): a four-workload suite job peaked at 25.5 GiB (canary c5b0d1c3, cap 35); three of them (77) plus four research jobs (9 measured, 14 cap each) do not fit in the ~88 GB available with the research cache loaded. S018 ran 3 x 22.3
        'judge_timeout_seconds': 14400, 'research_timeout_seconds': 3600,
        'problem_brief': HOME + '/PROBLEM-BRIEF.md', 'sme_brief': HOME + '/SME-BRIEF.md',
        'league_card': HOME + '/LEAGUE-CARD.md',
        'workflow': ['sme-owner', 'owner-draft', 'sme-coach', 'coach', 'inventors-round-1', 'builder-experiments',
                     'build-candidate (three builders at once, lead integrates)', 'judge'],
        # Operator 2026-09-28 ("Seems very lengthy"): no dialectic / second inventor round, builders start
        # together, owners draft at once, and each team runs its own match sequence with no barrier.
        'fast_chain': True, 'async_matches': True, 'parallel_owners': True,
        # Operator 2026-09-28 ("fix"): a team's own positively judged starting tree keeps its changes locked.
        'lock_won_changes': True,
        # Operator 2026-10-01: re-measure the top 3 nominees at 12 rounds before the one 42-round confirmation.
        'rescreen_top_n': 3,
        # Operator 2026-10-03 ("Do all work we want to do"), from S017 (INTERFACE.md amendment 2026-10-03 boundary):
        # research probes report a noise band and label readings inside it UNRESOLVED (optimizer survey #1); the
        # band uses the 2026-10-03 null (judge/NULL-20261003.md), not the 2026-09-24 one.
        'noise_aware_probes': True,
        # Operator 2026-10-04: builders' fast research tools (research-tools/CONTRACT.md): build_check, run_tests,
        # quick_bench, profile_diff, sweep_constant, race_check; fifth lane 2-5; probe budget.
        'fast_research_tools': True,
        # v2 tools (usage study 2026-10-04): sequential quick_bench, pre-measured nulls, ablate_changes, oracle_check,
        # phase_timeline, go_bench, closed_ground_search, try_merge, queue_status; harness research_tools_v2.py.
        'fast_tools_v2': True,
        # Role (Codex) processes and anything they spawn stay off the judge and research lanes and their SMT siblings.
        'role_cpus': '10,11,16-18,23,46,47,58,59,64-66,71,94,95',
        'probe_budget': {'benchmark_calls_per_turn': 3, 'rounds_need_unresolved': True},
        'probe_pair_sd_time_pct': NULL_20261003['time'], 'probe_pair_sd_score_pct': NULL_20261003['time'],
        'probe_pair_sd_rss_pct': NULL_20261003['rss'], 'probe_phase_pair_sd_pct': NULL_20261003['phase'],
        'probe_null_source': NULL_20261003['source'],
        # per-phase archive + one automatic merge of the season's best disjoint trees (survey #3).
        'season_merge': True, 'season_merge_band_pct': 0.5, 'season_merge_max_sources': 3,
        # Operator 2026-10-03: "have the s17 merge also include s16s candidates work". Carried forward as a rule: at each
        # boundary, list the previous season's best UNPROMOTED team trees here with the incumbent they were built on as
        # 'base' ({'candidate','base','team','match','season','gain_pct'}); the merge folds their changes onto the current
        # incumbent. The S016 entries used in S017 were removed 2026-10-04; fill in S017's at the S017/S018 boundary.
        # S018: empty (S017's best unpromoted trees went into the hand merge incumbents/S017-merge, merge-S017/REPORT.md).
        # S019 (boundary 2026-10-05): S018's best unpromoted trees, built on incumbents/S017-merge (T1's tree was promoted
        # as incumbents/S018); refill at the S019/S020 boundary.
        # S020 (boundary 2026-10-06): empty; the coordinator fills it after the S019 hand merge (merge-S019/).
        'season_merge_extra_sources': [],
        # Official judging on four visible workloads (judge/SUITE.md; from S019 `remote`, judge/REMOTE.md: main's corpus
        # through the REST backend on a simulated link, 30 ms / 48 MiB/s / 5 connections): score = mean of the
        # per-workload time saved. `remote` has its own probe bands (the other workloads use the main values).
        'workload_suite': ['main', 'big-index', 'large-files', 'remote'],
        'probe_workload_pair_sd_pct': {'remote': NULL_REMOTE_V2},
        'inventor_count_per_team': 3, 'builder_count_per_team': 3, 'persistent_sme_per_team': True,
        'builder_catalog': 'workspace/profiles/builders/catalog.json',
        'evidence_policy': 'real-process-and-benchmark-evidence',
        # The mature pylint format (S026) with the pylint-only parts off.
        'team_pipeline': True, 'judge_as_ready': True, 'unchanged_scores_zero': True, 'canonical_research_tools': True,
        'inventor_tools': 'bounded', 'persona_style': 'character', 'character_persona_revision': 'character-v2',
        'owner_coach_context': 'league', 'builder_mode': 'team-bridge', 'parallel_final_review': True,
        'research_process_style': 'constructive', 'targeted_consultations': True,
        'consultation_limit_per_team_match': 3, 'match_continuity': True, 'knowledge_digest': True,
        'owner_address_refresh': False, 'must_ship': True, 'explicit_final_deliverable': True,
        'candidate_diff_check': True, 'single_fresh_confirmation': True, 'bounded_turn_guidance': True,
        'continuation_context': 'full', 'sme_owner_brief_style': 'original',
        'lean_context': True, 'report_chars': 9000, 'final_from_fold': True,
        # Request budget per role: 24 tool rounds + 4 final (Go builders need more than pylint's 12).
        'max_source_requests': 24, 'max_read_lines': 300, 'max_read_chars': 24000, 'tool_result_chars': 24000,
        'tool_final_reserve_seconds': 90, 'callback_transport_reserve_seconds': 30,
        'api_final_reserve_seconds': 90, 'api_final_grace_seconds': 600,
        'match_ranking': 'subfactor_points', 'subfactor_min_wins': 4,
        # Pylint-only machinery off: cProfile pools, the pylint maintenance band, the pylint calibration constant.
        'post_match_profile': False, 'maintenance_merge': {'enabled': False},
        # First season characterises the instrument: confirm the best nominee regardless (gate off).
        'confirmation_gate': {'enabled': False, 'floor_pct': 0.3, 'force_confirmation_every_n_seasons': 5},
        'calibration_tolerance_pct': 1.0,
        'max_spend_usd': 1.0,
        'research_measurement_note': ('Research tools are the restic judge in correctness mode (check_parity) and one-'
                                      'round probe mode (benchmark_candidate). A probe beside official judging shares '
                                      'memory bandwidth: its numbers are diagnostic only.'),
        'lessons_note': ('Carried from the pylint league and the failed restic league (INTERFACE.md): pinned stock, frozen '
                         'workloads, pristine inputs per arm per round, oracle before timing, paired order-rotated '
                         'rounds, medians, win counts, a null run, no historical ledger mixing.'),
    }


def build_config(season, transport='space-bunny', teams=('TA',), matches=4, midseason_after='M002', cap=1.0):
    config = base_config()
    config.update(TRANSPORTS[transport])
    if season >= ROTATION_FROM:
        config.update(ROTATION)
    teams = [t.strip() for t in teams if t.strip()]
    config.update({
        'teams': teams, 'matches_per_season': int(matches), 'max_spend_usd': float(cap),
        'campaign_id': 'restic-v2-%s-%s' % (season, transport),
        'state': '%s-prepared-%s' % (season, transport),
        'run_scope': ('%s %s: %s, %d matches%s, retained team code, one fresh confirmation, knowledge carryover'
                      % (('Solo' if len(teams) == 1 else '%d-team' % len(teams)), season, ', '.join(teams), int(matches),
                         (' with a mid-season review after ' + midseason_after) if midseason_after != 'none' else '')),
    })
    if len(teams) == 1:
        config['special_season'] = {'kind': 'solo', 'teams': teams,
                                    'reason': 'operator decision 2026-09-24: the first Restic V2 season is solo on '
                                              + config['required_model']}
    if midseason_after and midseason_after != 'none' and int(matches) >= 2:
        after = 'M%03d' % int(str(midseason_after).lstrip('Mm'))
        config['midseason_review'] = {'after_match': after, 'directive': '',
                                      'directive_path': CONTROLLER + '/runtime/seasons/' + season + '-midseason-directive.md',
                                      'note': 'Matches after the review exist to stack what measured into the best confirmed candidate.'}
    return config


def local_checks(config):
    """What can be checked without the box: adapter selection, route, research tools, briefs, config shape."""
    from league_targets import target_for, ResticTarget
    from api_routes import route_for_model
    from agent_api_runner import tool_schemas
    from common_guard import required_stages
    checks = {}
    target = target_for(config)
    checks['adapter_is_restic'] = isinstance(target, ResticTarget)
    checks['objective_key'] = target.objective_key
    if config['backend'] == 'openrouter':
        route = route_for_model(config['model_id'])
        checks['route'] = {k: route.get(k) for k in ('backend', 'provider', 'max_tokens', 'free_daily_requests')}
    spec = target.research_spec()
    checks['builder_tools'] = [t['function']['name'] for t in tool_schemas('builder-1', False, True, True, spec)]
    checks['briefs_present'] = all((HERE / name).is_file() for name in BRIEFS)
    checks['required_stages_per_match'] = required_stages(config)
    checks['phases'] = spec['phases']
    checks['promotion_policy'] = target.promotion_policy()
    checks['ok'] = bool(checks['adapter_is_restic'] and checks['briefs_present']
                        and 'check_parity' in checks['builder_tools'] and 'run_experiment' not in checks['builder_tools'])
    return checks


def remote_run(source, timeout=600):
    """Upload a Python script in chunks (the base64 pipe truncates long scripts) and run it on the box."""
    from remote_broker import remote_python
    remote = '/tmp/restic-v2-launch-%d.py' % int(time.time() * 1000)
    remote_python('open(%r,"w").close()' % remote, timeout=120)
    for start in range(0, len(source), 5000):
        remote_python('open(%r,"a",encoding="utf-8").write(%r)' % (remote, source[start:start + 5000]), timeout=120)
    try:
        raw = remote_python('exec(compile(open(%r,encoding="utf-8").read(),%r,"exec"))' % (remote, remote), timeout=timeout)
    finally:
        # The script may carry the API key (launch/canary environment); never leave it in /tmp.
        remote_python('import os,pathlib; pathlib.Path(%r).unlink(missing_ok=True)' % remote, timeout=120)
    return json.loads(raw.rsplit('RESULT_JSON:', 1)[1])


STAGE = r'''
import json,os,pathlib,shutil,tarfile,time,sys
BOX=pathlib.Path(__BOX__);R=pathlib.Path(__CTRL__);HOME=pathlib.Path(__HOME__);WORK=pathlib.Path(__WORK__)
season=__SEASON__;names=__NAMES__
bundle=pathlib.Path(__UPLOAD__)/('restic-v2-controller-'+season+'.tar.gz')
R.mkdir(parents=True,exist_ok=True)
with tarfile.open(bundle) as archive:
    if any(not m.isfile() or m.name not in names for m in archive.getmembers()):raise ValueError('Unexpected member')
    for m in archive.getmembers():
        if m.name.endswith('.py'):compile(archive.extractfile(m).read(),m.name,'exec')
    archive.extractall(R,filter='data')
sys.path.insert(0,str(R/'tools'));from agent_runner import atomic
report={'controller_root':str(R)}
if not (R/'shared').is_dir():
    shutil.copytree(__PYSHARED__,R/'shared');report['shared']='copied from the pylint league'
if not (WORK/'profiles').is_dir():
    shutil.copytree(__PYWORK__+'/profiles',WORK/'profiles');report['profiles']='copied from the pylint league'
stock=BOX/'stock'
from league_targets import tree_files,manifest_identity
started=any((WORK/'teams').glob('*/S*')) if (WORK/'teams').is_dir() else False
if (WORK/'upstream'/'go.mod').is_file() and not started:
    # upstream must be byte-identical to stock under the judge's source rule (2026-09-24: the first stock/ was a
    # CRLF git archive and was replaced by the true LF upstream bytes; a stale copy shows every .go as changed).
    if manifest_identity(tree_files(WORK/'upstream'))!=manifest_identity(tree_files(stock)):
        old=WORK/('upstream.superseded-%d'%int(time.time()));os.replace(WORK/'upstream',old)
        shutil.rmtree(old,ignore_errors=True);report['upstream_refreshed']='identity differed from stock; recopied'
if not (WORK/'upstream'/'go.mod').is_file():
    if not (stock/'go.mod').is_file():raise RuntimeError('restic stock is not a module root: '+str(stock))
    tmp=WORK/('upstream.tmp-%d'%os.getpid())
    shutil.copytree(stock,tmp,ignore=shutil.ignore_patterns('.git'),symlinks=False)
    os.replace(tmp,WORK/'upstream');report['upstream']='copied from '+str(stock)
(WORK/'teams').mkdir(parents=True,exist_ok=True);(HOME/'knowledge').mkdir(parents=True,exist_ok=True)
cfg=json.loads((R/'runtime/seasons'/(season+'-config.json')).read_text())
roster_path=WORK/'roster.json'
if not roster_path.is_file():
    src=json.loads(pathlib.Path(__PYWORK__+'/roster.json').read_text())
    owner=dict(src['roles']['owners'].get('TA') or next(p for p in src.get('owner_bench',[]) if p.get('persona_id')=='O12'))
    for key in ('id','profile','special','seated_after','replaced','strikes','status','seasons_bottom'):owner.pop(key,None)
    owner.update(model=cfg['required_model'],strikes=0,status='active',seated_after='Restic V2 founding',
                 special='seated for the solo first Restic V2 season; a copy of the pylint league TA owner persona')
    bench=[dict(p) for p in src.get('owner_bench',[])]
    roster={'league':cfg['id'],'active_season':season,'required_model':cfg['required_model'],
            'stock_pin':cfg['source_pins']['restic'],
            'roles':{'owners':{t:dict(owner) for t in cfg['teams']},
                     'subject_matter_experts':{t:{'name':t+' Problem Subject Matter Expert','model':cfg['required_model'],
                                                  'status':'pending','supports':['owner','coach']} for t in cfg['teams']},
                     'builders':{t:{'count':cfg['builder_count_per_team'],'selection':'owner drafts; coach may substitute between matches',
                                    'status':'pending','roster':[]} for t in cfg['teams']},
                     'coaches':{'model':cfg['required_model'],'status':'drafted by owners'}},
            'owner_bench':bench,'completed_seasons':[],
            'note':'Restic League V2 roster; owners/bench copied from the pylint league persona roster, strikes reset.'}
    atomic(roster_path,roster);report['roster']='seated '+', '.join(cfg['teams'])+' with '+owner['name']
# Later seasons may add teams (four-team Luna seasons): seat each missing team from the pylint league's owner for that
# team id, else the next unused bench persona; existing owners, strikes and history are kept.
roster=json.loads(roster_path.read_text());roles=roster['roles'];added=[]
src=json.loads(pathlib.Path(__PYWORK__+'/roster.json').read_text())
used={o.get('persona_id') for o in roles['owners'].values()}
for t in cfg['teams']:
    if t in roles['owners']:continue
    cand=src['roles']['owners'].get(t) or next((p for p in src.get('owner_bench',[]) if p.get('persona_id') not in used),None)
    if cand is None:raise RuntimeError('no owner persona available for '+t)
    o=dict(cand)
    for key in ('id','profile','special','seated_after','replaced','strikes','status','seasons_bottom'):o.pop(key,None)
    o.update(model=cfg['required_model'],strikes=0,status='active',seated_after='Restic V2 '+season)
    roles['owners'][t]=o;used.add(o.get('persona_id'))
    roles['subject_matter_experts'][t]={'name':t+' Problem Subject Matter Expert','model':cfg['required_model'],'status':'pending','supports':['owner','coach']}
    roles['builders'][t]={'count':cfg['builder_count_per_team'],'selection':'owner drafts; coach may substitute between matches','status':'pending','roster':[]}
    added.append(t+'='+o.get('name','?'))
if added:
    roster['active_season']=season;atomic(roster_path,roster);report['roster_added']=added
atomic(HOME/'league.json',cfg)
registry_path=R/'league-registry.json'
registry=json.loads(registry_path.read_text()) if registry_path.is_file() else {'leagues':[]}
registry['leagues']=[x for x in registry['leagues'] if x['id']!=cfg['id']]+[cfg];atomic(registry_path,registry)
judge=pathlib.Path(cfg['restic_judge']);iso=pathlib.Path(cfg['isolation_evidence'])
report.update(judge_present=judge.is_file(),isolation_present=iso.is_file(),
              isolation_verified=(json.loads(iso.read_text()).get('verified') is True) if iso.is_file() else False,
              upstream_files=sum(1 for _ in (WORK/'upstream').rglob('*')),
              roster_owner=json.loads(roster_path.read_text())['roles']['owners'])
print('RESULT_JSON:'+json.dumps(report,default=str))
'''

ALIVE = r'''
def alive(pid,marker):
    try:return marker in open('/proc/%d/cmdline'%int(pid),'rb').read().decode(errors='replace')
    except (OSError,TypeError,ValueError):return False
'''

START = r'''
import json,os,pathlib,subprocess,time
R=pathlib.Path(__CTRL__);season=__SEASON__;cfg_path=R/'runtime/seasons'/(season+'-config.json')
cfg=json.loads(cfg_path.read_text())
''' + ALIVE + r'''
seasons=R/'evidence/seasons'/cfg['id']
resume=__RESUME__
import signal
for other in seasons.glob('S*/status.json'):
    s=json.loads(other.read_text())
    if alive(s.get('pid'),'run_season.py'):
        if not (resume and other.parent.name==season):
            raise RuntimeError(other.parent.name+' controller is alive (pid %s)'%s.get('pid'))
        os.kill(s['pid'],signal.SIGTERM)
        for _ in range(120):
            time.sleep(1)
            if not alive(s['pid'],'run_season.py'):break
        else:raise RuntimeError('controller %s did not stop within 120 s'%s['pid'])
if (seasons/season/'status.json').exists() and not resume:raise RuntimeError(season+' already exists; use --resume')
if not pathlib.Path(cfg['restic_judge']).is_file():raise RuntimeError('restic judge missing: '+cfg['restic_judge'])
log=(R/'runtime/seasons'/(season+'.log')).open('ab',buffering=0)
command=['/usr/bin/python3','-u',str(R/'tools/run_season.py'),'--config',str(cfg_path),'--season',season]+(['--resume'] if resume else [])
p=subprocess.Popen(command,cwd=R,env={**os.environ,**__ENV__},stdin=subprocess.DEVNULL,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
receipt={'status':'resumed' if resume else 'launched','pid':p.pid,'started_at':time.time(),'season':season,'model':cfg['model_id'],
         'backend':cfg['backend'],'teams':cfg['teams'],'config':str(cfg_path),'log':str(R/'runtime/seasons'/(season+'.log')),
         'status_file':str(seasons/season/'status.json'),'api_cap_usd':cfg['max_spend_usd']}
(R/'runtime/seasons'/(season+'-launch.json')).write_text(json.dumps(receipt,indent=2))
print('RESULT_JSON:'+json.dumps(receipt))
'''


def stage(season, config):
    from remote_broker import transfer
    out = HERE / 'runtime'
    out.mkdir(exist_ok=True)
    config_path = out / (season + '-config.json')
    config_path.write_text(json.dumps(config, indent=2), encoding='utf-8')
    bundle = out / (season + '-controller.tar.gz')
    names = []
    with tarfile.open(bundle, 'w:gz') as archive:
        for name in TOOLS:
            archive.add(LEAGUES / 'tools' / name, arcname='tools/' + name, recursive=False)
            names.append('tools/' + name)
        for name in BRIEFS:
            archive.add(HERE / name, arcname=LEAGUE_ID + '/' + name, recursive=False)
            names.append(LEAGUE_ID + '/' + name)
        for name in SCRIPTS:
            archive.add(HERE / name, arcname='runtime/restic/' + name, recursive=False)
            names.append('runtime/restic/' + name)
        archive.add(config_path, arcname='runtime/seasons/' + config_path.name, recursive=False)
        names.append('runtime/seasons/' + config_path.name)
    # box_transfer.ps1 only writes under the pylint league's input root; the stage script reads it from there.
    transfer('upload', bundle, UPLOAD_ROOT + '/restic-v2-controller-' + season + '.tar.gz', timeout=300)
    source = (STAGE.replace('__BOX__', repr(BOX_ROOT)).replace('__CTRL__', repr(CONTROLLER))
              .replace('__HOME__', repr(HOME)).replace('__WORK__', repr(WORK)).replace('__SEASON__', repr(season))
              .replace('__NAMES__', repr(names)).replace('__PYSHARED__', repr(PYLINT_SHARED))
              .replace('__PYWORK__', repr(PYLINT_WORK)).replace('__UPLOAD__', repr(UPLOAD_ROOT)))
    return remote_run(source, timeout=900)


def key_env(config):
    if config['backend'] == 'openrouter':
        from agent_api_runner import _load_key
        return {'OPENROUTER_API_KEY': _load_key('openrouter')}
    return {'LEAGUE_ROLE_CPUS': config['role_cpus']} if config.get('role_cpus') else {}


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--season', default='S001')
    parser.add_argument('--transport', choices=sorted(TRANSPORTS), default='space-bunny')
    parser.add_argument('--teams', default='TA')
    parser.add_argument('--matches', type=int, default=4)
    parser.add_argument('--midseason-after', default='M002')
    parser.add_argument('--cap', type=float, default=1.0, help='API spend tripwire in USD (space-bunny is free)')
    mode = parser.add_mutually_exclusive_group(required=True)
    for flag in ('--dry-run', '--stage', '--canary', '--box-dry-run', '--start', '--resume'):
        mode.add_argument(flag, action='store_true')
    parser.add_argument('--canary-judge', action='store_true', help='canary also calls check_parity through the real judge')
    parser.add_argument('--directive-file', default=None,
                        help='season directive (markdown) written into the briefs before staging; replaces the previous one')
    args = parser.parse_args()
    config = build_config(args.season, args.transport, args.teams.split(','), args.matches, args.midseason_after, args.cap)
    (HERE / 'league.json').write_text(json.dumps(base_config(), indent=2), encoding='utf-8')
    checks = local_checks(config)
    if args.dry_run:
        (HERE / 'runtime').mkdir(exist_ok=True)
        (HERE / 'runtime' / (args.season + '-config.json')).write_text(json.dumps(config, indent=2), encoding='utf-8')
        print(json.dumps({'season': args.season, 'transport': args.transport, 'checks': checks,
                          'config': str(HERE / 'runtime' / (args.season + '-config.json'))}, indent=2, default=str))
        return 0 if checks['ok'] else 1
    if not checks['ok']:
        raise SystemExit('local checks failed: ' + json.dumps(checks, default=str))
    if args.directive_file:
        set_directive(pathlib.Path(args.directive_file).read_text(encoding='utf-8'))
    report = {'stage': stage(args.season, config)}
    if args.canary:
        env = key_env(config)
        source = ('import os,subprocess,json\n'
                  'env={**os.environ,**%r,"LEAGUE_MAX_SPEND_USD":"1"}\n'
                  'p=subprocess.run(["/usr/bin/python3",%r,"--config",%r]+%r,cwd=%r,env=env,capture_output=True,text=True,timeout=3200)\n'
                  'print(p.stdout[-6000:]);print(p.stderr[-3000:])\n'
                  % (env, CONTROLLER + '/runtime/restic/canary_role.py',
                     CONTROLLER + '/runtime/seasons/' + args.season + '-config.json',
                     ['--judge'] if args.canary_judge else [], CONTROLLER))
        report["canary"] = remote_run(source, timeout=3400)
    elif args.box_dry_run:
        source = ('import subprocess\n'
                  'p=subprocess.run(["/usr/bin/python3",%r,"--config",%r],cwd=%r,capture_output=True,text=True,timeout=900)\n'
                  'print(p.stdout[-8000:]);print(p.stderr[-3000:])\n'
                  % (CONTROLLER + '/runtime/restic/dry_run_box.py', CONTROLLER + '/runtime/seasons/' + args.season + '-config.json',
                     CONTROLLER))
        report['box_dry_run'] = remote_run(source, timeout=1000)
    elif args.start or args.resume:
        source = (START.replace('__CTRL__', repr(CONTROLLER)).replace('__SEASON__', repr(args.season))
                  .replace('__RESUME__', repr(bool(args.resume))).replace('__ENV__', repr(key_env(config))))
        report['launch'] = remote_run(source, timeout=300)
        (HERE / 'runtime' / (args.season + '-launch.json')).write_text(json.dumps(report['launch'], indent=2), encoding='utf-8')
    print(json.dumps(report, indent=2, default=str))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
