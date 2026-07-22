From Coq Require Import Arith.
Require Import Reference.

Example triangular_zero_contract : Reference.triangular 0 = 0 := eq_refl.

Example triangular_step_contract :
  forall n,
    Reference.triangular (S n) = S n + Reference.triangular n :=
  fun n => eq_refl.

Definition triangular_target_contract :
  forall n, 2 * Reference.triangular n = n * (n + 1) :=
  Reference.triangular_closed_form.

Print Assumptions Reference.triangular_closed_form.
