"""Pure negative checks for the release cutover gate: no live I/O."""
import sys
import unittest
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT/'scripts'))
from qual24_goose13_cutover_probe import (
    LEGACY_DONE,PENDING_ITEM,validate_cutover
)


def valid():
    return (
        {'default_mode':'independent_review','owner_acceptance_allowed':False,'version':0},
        {'work_item_id':LEGACY_DONE,'work_status':'done','acceptance_mode':'legacy_unclassified','decision':None},
        {'work_item_id':PENDING_ITEM,'work_status':'todo','work_version':5,'acceptance_mode':'not_completed','decision':None},
        {'work_item_id':PENDING_ITEM,'mode':'independent_review','owner_acceptance_allowed':False},
        {'id':PENDING_ITEM,'status':'todo','version':5},
    )


class CutoverGateTests(unittest.TestCase):
    def test_only_actual_new_route_consistency_passes(self):
        self.assertEqual(validate_cutover(*valid()),[])

    def test_a_green_server_with_no_history_cannot_pass(self):
        x=list(valid());x[1]=None
        self.assertTrue(validate_cutover(*x))

    def test_old_done_cannot_infer_independent_review(self):
        x=list(valid());x[1]={**x[1],'acceptance_mode':'independent_review','decision':{'independent':True}}
        self.assertTrue(validate_cutover(*x))

    def test_pending_case_version_conflict_blocks_cutover(self):
        x=list(valid());x[2]={**x[2],'work_version':4}
        self.assertTrue(validate_cutover(*x))

    def test_project_owner_self_signoff_is_not_default(self):
        x=list(valid());x[0]={**x[0],'owner_acceptance_allowed':True}
        self.assertTrue(validate_cutover(*x))

    def test_high_risk_policy_override_is_not_inferred(self):
        x=list(valid());x[3]={**x[3],'mode':'owner_acceptance'}
        self.assertTrue(validate_cutover(*x))

    def test_completed_pending_work_requires_new_context_not_old_sample(self):
        x=list(valid());x[4]={**x[4],'status':'done'}
        self.assertTrue(validate_cutover(*x))


if __name__=='__main__':
    unittest.main()
