From Coq Require Import List Lia.
Import ListNotations.

Fixpoint reverse_accumulator {A : Type} (values accumulator : list A) : list A :=
  match values with
  | [] => accumulator
  | value :: remaining => reverse_accumulator remaining (value :: accumulator)
  end.

Theorem reverse_accumulator_correct :
  forall (A : Type) (values accumulator : list A),
    length (reverse_accumulator values accumulator) = length values + length accumulator.
Proof.
  intros A values.
  induction values as [|value remaining induction_hypothesis].
  - reflexivity.
  - intros accumulator. simpl. rewrite induction_hypothesis. lia.
Qed.
