import importlib.util
import unittest
from pathlib import Path

module_path=Path(__file__).resolve().parents[1]/"qual-review-lane-smoke.py"
spec=importlib.util.spec_from_file_location("qual_review_lane_smoke",module_path)
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class ReviewLaneClassificationTests(unittest.TestCase):
    def test_in_review_needs_work_is_rejected_not_awaiting(self):
        items={"items":[{"key":"QUAL-4","status":"in_review","latest_review":{"outcome":"needs_work","self_review":True},"review_lane":"needs_work"}]}
        self.assertEqual(module.validate_review_queue(items),[])
        items["items"][0]["review_lane"]="awaiting"
        self.assertIn("expected review_lane=needs_work",module.validate_review_queue(items)[0])
    def test_independent_pass_is_not_done_and_self_pass_still_awaits(self):
        data={"items":[
          {"key":"QUAL-PASS","status":"in_review","latest_review":{"outcome":"passed","self_review":False},"review_lane":"ready_to_close"},
          {"key":"QUAL-SELF","status":"in_review","latest_review":{"outcome":"passed","self_review":True},"review_lane":"awaiting"}]}
        self.assertEqual(module.validate_review_queue(data),[])
    def test_missing_lane_fails_real_contract(self):
        self.assertTrue(module.validate_review_queue({"items":[{"key":"QUAL-8","status":"in_progress","latest_review":{"outcome":"needs_work"}}]}))
if __name__=="__main__":unittest.main()
